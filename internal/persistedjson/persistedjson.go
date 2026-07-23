// Package persistedjson implements cc-review's exact fingerprinted JSON v1 envelope.
package persistedjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const schemaVersion = 1

type envelope[Payload any] struct {
	Schema            *string  `json:"schema"`
	SchemaVersion     *int     `json:"schemaVersion"`
	SchemaFingerprint *string  `json:"schemaFingerprint"`
	Payload           *Payload `json:"payload"`
}

// Encode wraps payload in the exact persisted v1 envelope.
func Encode[Payload any](identity, fingerprint string, payload Payload) ([]byte, error) {
	return json.Marshal(struct {
		Schema            string  `json:"schema"`
		SchemaVersion     int     `json:"schemaVersion"`
		SchemaFingerprint string  `json:"schemaFingerprint"`
		Payload           Payload `json:"payload"`
	}{
		Schema:            identity,
		SchemaVersion:     schemaVersion,
		SchemaFingerprint: fingerprint,
		Payload:           payload,
	})
}

// Decode accepts only the exact persisted v1 envelope and payload shape.
func Decode[Payload any](data []byte, identity, fingerprint string) (Payload, error) {
	var zero Payload
	if err := rejectDuplicateObjectKeys(data); err != nil {
		return zero, err
	}
	var decoded envelope[Payload]
	if err := DecodeValue(data, &decoded); err != nil {
		return zero, err
	}
	if decoded.Schema == nil || *decoded.Schema != identity {
		return zero, fmt.Errorf("schema must equal %q", identity)
	}
	if decoded.SchemaVersion == nil || *decoded.SchemaVersion != schemaVersion {
		return zero, fmt.Errorf("schemaVersion must equal %d", schemaVersion)
	}
	if decoded.SchemaFingerprint == nil || *decoded.SchemaFingerprint != fingerprint {
		return zero, fmt.Errorf("schemaFingerprint must equal %q", fingerprint)
	}
	if decoded.Payload == nil {
		return zero, errors.New("payload is required and must not be null")
	}
	return *decoded.Payload, nil
}

// DecodeValue strictly decodes one JSON value with no unknown fields or trailing data.
func DecodeValue(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// WriteFile atomically publishes a private durable JSON document.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".persisted-json-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	directory, err := os.Open(dir) //nolint:gosec // caller owns the persisted-state path.
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close parent directory: %w", err)
	}
	return nil
}

func rejectDuplicateObjectKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return scanJSONValue(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return fmt.Errorf("unexpected JSON delimiter %q", closing)
	}
	return nil
}
