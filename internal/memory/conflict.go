package memory

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type MemoryConflict struct {
	ID         int64      `json:"id"`
	LeftRef    string     `json:"leftRef"`
	RightRef   string     `json:"rightRef"`
	Status     string     `json:"status"`
	Actor      string     `json:"actor"`
	Reason     string     `json:"reason"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedBy string     `json:"resolvedBy,omitempty"`
	Resolution string     `json:"resolution,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

func parseReference(ref string) (string, int64, error) {
	kind, idText, ok := strings.Cut(ref, ":")
	if !ok {
		return "", 0, fmt.Errorf("taxion: invalid Memory reference %q (want kind:id)", ref)
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || fmt.Sprintf("%s:%d", kind, id) != ref || id < 1 || checkKind(kind) != nil {
		return "", 0, fmt.Errorf("taxion: invalid Memory reference %q (want kind:id)", ref)
	}
	return kind, id, nil
}

func scanConflict(row interface{ Scan(...any) error }) (MemoryConflict, error) {
	var conflict MemoryConflict
	var created, resolved string
	if err := row.Scan(&conflict.ID, &conflict.LeftRef, &conflict.RightRef, &conflict.Status,
		&conflict.Actor, &conflict.Reason, &created, &conflict.ResolvedBy,
		&conflict.Resolution, &resolved); err != nil {
		return MemoryConflict{}, err
	}
	var err error
	conflict.CreatedAt, err = parseTs(created)
	if err != nil {
		return MemoryConflict{}, fmt.Errorf("decode Memory conflict creation time: %w", err)
	}
	if resolved != "" {
		at, parseErr := parseTs(resolved)
		if parseErr != nil {
			return MemoryConflict{}, fmt.Errorf("decode Memory conflict resolution time: %w", parseErr)
		}
		conflict.ResolvedAt = &at
	}
	return conflict, nil
}

const conflictSelect = `id,left_ref,right_ref,status,actor,reason,created_at,resolved_by,resolution,resolved_at`

func conflictRefs(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func conflictEntry(tx *sql.Tx, ref string) (Entry, error) {
	kind, id, err := parseReference(ref)
	if err != nil {
		return Entry{}, err
	}
	entry, err := scanEntry(tx.QueryRow(
		fmt.Sprintf(`SELECT %s FROM %s WHERE id=?`, entrySelect, tableFor(kind)), id))
	if err == sql.ErrNoRows {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return entry, err
}

// AddConflict records a human-identified conflict; no semantic inference is performed.
func (s *Store) AddConflict(leftRef, rightRef, actor, reason string) (int64, error) {
	if leftRef == rightRef || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return 0, fmt.Errorf("taxion: conflict requires two different Memory references, actor, and reason")
	}
	leftKind, leftID, err := parseReference(leftRef)
	if err != nil {
		return 0, err
	}
	rightKind, rightID, err := parseReference(rightRef)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	left, err := conflictEntry(tx, leftRef)
	if err != nil {
		return 0, err
	}
	right, err := conflictEntry(tx, rightRef)
	if err != nil {
		return 0, err
	}
	for _, entry := range []Entry{left, right} {
		if entry.Status == "revoked" || entry.Status == "superseded" ||
			(entry.Status != "proposed" && entry.Status != "approved") {
			return 0, fmt.Errorf("taxion: cannot record a conflict with inactive Memory entry")
		}
	}
	first, second := conflictRefs(leftRef, rightRef)
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO memory_conflicts(left_ref,right_ref,status,actor,reason,created_at)
		 VALUES(?,?,'active',?,?,?)`, first, second, actor, reason, ts(now))
	if err != nil {
		return 0, fmt.Errorf("taxion: record Memory conflict: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, leftKind, leftID,
		"conflict-recorded", actor, fmt.Sprintf("conflict %d with %s: %s", id, rightRef, reason), now); err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, rightKind, rightID,
		"conflict-recorded", actor, fmt.Sprintf("conflict %d with %s: %s", id, leftRef, reason), now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Conflicts lists active and resolved relations involving a Memory reference.
func (s *Store) Conflicts(ref string) ([]MemoryConflict, error) {
	if _, _, err := parseReference(ref); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT `+conflictSelect+` FROM memory_conflicts
		 WHERE left_ref=? OR right_ref=? ORDER BY id`, ref, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conflicts []MemoryConflict
	for rows.Next() {
		conflict, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

// ResolveConflict closes a recorded relation only with an explicit human resolution.
func (s *Store) ResolveConflict(id int64, actor, resolution string) error {
	if id < 1 || strings.TrimSpace(actor) == "" || strings.TrimSpace(resolution) == "" {
		return fmt.Errorf("taxion: conflict resolution requires a positive ID, actor, and resolution")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	conflict, err := scanConflict(tx.QueryRow(
		`SELECT `+conflictSelect+` FROM memory_conflicts WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: Memory conflict %d", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if conflict.Status != "active" {
		return fmt.Errorf("taxion: Memory conflict %d is already resolved", id)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE memory_conflicts SET status='resolved',resolved_by=?,resolution=?,resolved_at=? WHERE id=?`,
		actor, resolution, ts(now), id); err != nil {
		return err
	}
	for _, ref := range []string{conflict.LeftRef, conflict.RightRef} {
		kind, entryID, err := parseReference(ref)
		if err != nil {
			return err
		}
		if err := recordEntryEvent(tx, kind, entryID, "conflict-resolved", actor,
			fmt.Sprintf("conflict %d resolved: %s", id, resolution), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
