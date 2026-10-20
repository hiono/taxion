package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalJSON renders v as deterministic JSON: object keys in byte order, no
// insignificant whitespace, HTML escaping disabled and UTF-8 preserved. The
// same value always produces the same bytes on every platform, so its hash can
// be used as an approval digest.
func CanonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(tree); err != nil {
		return nil, err
	}
	encoded := bytes.TrimRight(buffer.Bytes(), "\n")
	encoded = unescapeLineSeparators(encoded)
	return encoded, nil
}

// CanonicalSHA256 returns the lowercase hexadecimal SHA-256 of CanonicalJSON(v).
func CanonicalSHA256(v any) (string, error) {
	data, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

// SHA256Hex returns the lowercase hexadecimal SHA-256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// unescapeLineSeparators replaces the escape sequences for U+2028 and U+2029
// that encoding/json always emits. Only genuine escape tokens are replaced: a
// literal backslash-u sequence in the input arrives as a doubled backslash and
// is left untouched.
func unescapeLineSeparators(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != '\\' {
			out = append(out, data[i])
			i++
			continue
		}
		j := i
		for j < len(data) && data[j] == '\\' {
			j++
		}
		if (j-i)%2 == 1 && j+5 <= len(data) && data[j] == 'u' {
			sequence := string(data[j+1 : j+5])
			if sequence == "2028" || sequence == "2029" {
				out = append(out, data[i:j-1]...)
				if sequence == "2028" {
					out = append(out, string(rune(0x2028))...)
				} else {
					out = append(out, string(rune(0x2029))...)
				}
				i = j + 5
				continue
			}
		}
		out = append(out, data[i:j]...)
		i = j
	}
	return out
}
