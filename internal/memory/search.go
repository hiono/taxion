package memory

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Hit is one search result. Rank is the BM25 score on the FTS path
// (lower is better) and 0 on the LIKE path.
type Hit struct {
	Kind  string
	ID    int64
	Title string
	Scope string
	Rank  float64
}

const ftsSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
  title, body, kind UNINDEXED, ref_id UNINDEXED, scope UNINDEXED,
  tokenize='trigram');
`

// Triggers cover both Put and promotion without duplicating index updates
// in callers. Status-only approval leaves indexed fields unchanged; changes
// to title/body/scope need an UPDATE trigger before adding such a path.
const ftsTriggers = `
CREATE TRIGGER IF NOT EXISTS lessons_ai AFTER INSERT ON lessons BEGIN
  INSERT INTO memory_fts(title,body,kind,ref_id,scope)
  VALUES(new.title,new.body,'lesson',new.id,new.scope);
END;
CREATE TRIGGER IF NOT EXISTS lessons_ad AFTER DELETE ON lessons BEGIN
  DELETE FROM memory_fts WHERE kind='lesson' AND ref_id=old.id;
END;
CREATE TRIGGER IF NOT EXISTS decisions_ai AFTER INSERT ON decisions BEGIN
  INSERT INTO memory_fts(title,body,kind,ref_id,scope)
  VALUES(new.title,new.body,'decision',new.id,new.scope);
END;
CREATE TRIGGER IF NOT EXISTS decisions_ad AFTER DELETE ON decisions BEGIN
  DELETE FROM memory_fts WHERE kind='decision' AND ref_id=old.id;
END;
CREATE TRIGGER IF NOT EXISTS guardrails_ai AFTER INSERT ON guardrails BEGIN
  INSERT INTO memory_fts(title,body,kind,ref_id,scope)
  VALUES(new.title,new.body,'guardrail',new.id,new.scope);
END;
CREATE TRIGGER IF NOT EXISTS guardrails_ad AFTER DELETE ON guardrails BEGIN
  DELETE FROM memory_fts WHERE kind='guardrail' AND ref_id=old.id;
END;
CREATE TRIGGER IF NOT EXISTS concerns_ai AFTER INSERT ON concerns BEGIN
  INSERT INTO memory_fts(title,body,kind,ref_id,scope)
  VALUES(new.title,new.body,'concern',new.id,new.scope);
END;
CREATE TRIGGER IF NOT EXISTS concerns_ad AFTER DELETE ON concerns BEGIN
  DELETE FROM memory_fts WHERE kind='concern' AND ref_id=old.id;
END;
`

// Backfill an empty index so entries saved before FTS was added remain
// searchable after opening an existing database.
func (s *Store) ensureFTS() error {
	if _, err := s.db.Exec(ftsSchema); err != nil {
		return err
	}
	if _, err := s.db.Exec(ftsTriggers); err != nil {
		return err
	}
	var indexed int
	if err := s.db.QueryRow(`SELECT count(*) FROM memory_fts`).Scan(&indexed); err != nil {
		return err
	}
	if indexed > 0 {
		return nil
	}
	for _, kind := range []string{KindLesson, KindDecision, KindGuardrail, KindConcern} {
		if _, err := s.db.Exec(fmt.Sprintf(
			`INSERT INTO memory_fts(title,body,kind,ref_id,scope)
			  SELECT title,body,'%s',id,scope FROM %s`, kind, tableFor(kind))); err != nil {
			return err
		}
	}
	return nil
}

// Treat input as one literal phrase, not caller-supplied FTS operators.
func ftsEscape(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// Escape wildcards so the LIKE fallback keeps literal substring semantics.
func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(q)
}

// Search finds entries by query across the four tables, best first.
// Empty kind searches all kinds; empty scope searches all scopes.
// Queries under 3 characters use LIKE; FTS misses fall back to LIKE.
func (s *Store) Search(query, scope, kind string) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("taxion: search query must not be empty")
	}
	if kind != "" {
		if err := checkKind(kind); err != nil {
			return nil, err
		}
	}
	if scope != "" {
		if err := checkScope(scope); err != nil {
			return nil, err
		}
	}
	kinds := []string{KindLesson, KindDecision, KindGuardrail, KindConcern}
	if kind != "" {
		kinds = []string{kind}
	}
	// Trigrams cannot match fewer than three characters. FTS misses use
	// LIKE as well so a zero-result FTS path does not hide substring matches.
	if utf8.RuneCountInString(query) >= 3 {
		hits, err := s.searchFTS(query, scope, kinds)
		if err != nil {
			return nil, err
		}
		if len(hits) > 0 {
			return s.currentHits(hits)
		}
	}
	hits, err := s.searchLIKE(query, scope, kinds)
	if err != nil {
		return nil, err
	}
	return s.currentHits(hits)
}

func (s *Store) currentHits(hits []Hit) ([]Hit, error) {
	out := make([]Hit, 0, len(hits))
	for _, hit := range hits {
		entry, err := s.Get(hit.Kind, hit.ID)
		if err != nil {
			return nil, err
		}
		if entry.Status != "revoked" && entry.Status != "superseded" {
			out = append(out, hit)
		}
	}
	return out, nil
}

func ftsFilter(kinds []string, scope string) (string, []any) {
	var conds []string
	var args []any
	if len(kinds) == 1 {
		conds = append(conds, `kind=?`)
		args = append(args, kinds[0])
	}
	if scope != "" {
		conds = append(conds, `scope=?`)
		args = append(args, scope)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(conds, " AND "), args
}

func (s *Store) searchFTS(query, scope string, kinds []string) ([]Hit, error) {
	filter, args := ftsFilter(kinds, scope)
	args = append([]any{ftsEscape(query)}, args...)
	rows, err := s.db.Query(
		`SELECT kind,ref_id,title,scope,bm25(memory_fts) FROM memory_fts
		 WHERE memory_fts MATCH ?`+filter+` ORDER BY bm25(memory_fts)`, args...)
	if err != nil {
		return nil, err
	}
	return scanHits(rows)
}

func (s *Store) searchLIKE(query, scope string, kinds []string) ([]Hit, error) {
	filter, args := ftsFilter(kinds, scope)
	pat := "%" + likeEscape(query) + "%"
	var out []Hit
	for _, k := range kinds {
		rows, err := s.db.Query(fmt.Sprintf(
			`SELECT '%s',id,title,scope,0.0 FROM %s
			 WHERE (title LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`+filter,
			k, tableFor(k)), append([]any{pat, pat}, args...)...)
		if err != nil {
			return nil, err
		}
		hits, err := scanHits(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return out, nil
}

func scanHits(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]Hit, error) {
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Kind, &h.ID, &h.Title, &h.Scope, &h.Rank); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
