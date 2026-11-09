package portal

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"slices"
)

func hubDecode(w http.ResponseWriter, r *http.Request, value any, keys ...string) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		hubHTTPError(w, http.StatusUnsupportedMediaType, "invalid_parameters", "A JSON object is required")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Request body is unreadable or exceeds 32 KiB")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	start, err := d.Token()
	valid := err == nil && start == json.Delim('{')
	seen := make(map[string]bool)
	for valid && d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(keys, key) {
			valid = false
			break
		}
		seen[key] = true
		var raw json.RawMessage
		valid = d.Decode(&raw) == nil
	}
	if valid {
		end, err := d.Token()
		valid = err == nil && end == json.Delim('}') && d.Decode(new(any)) == io.EOF
	}
	if !valid || json.Unmarshal(body, value) != nil {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Expected one JSON object with unique, supported fields and valid field types")
		return false
	}
	return true
}
