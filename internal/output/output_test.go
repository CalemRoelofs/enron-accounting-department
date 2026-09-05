package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/output"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := output.WriteJSON(&buf, map[string]string{"status": "success"})
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	result := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(result, `{"status":"success"}`) {
		t.Errorf("unexpected output: %s", result)
	}
}

func TestWriteJSON_Array(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := output.WriteJSON(&buf, []map[string]any{
		{"id": 1, "name": "test"},
	})
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	result := strings.TrimSpace(buf.String())
	expected := `[{"id":1,"name":"test"}]`
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	output.WriteError(&buf, "something went wrong")

	result := strings.TrimSpace(buf.String())
	if !strings.Contains(result, `"status":"error"`) {
		t.Errorf("expected error status in %s", result)
	}
	if !strings.Contains(result, `"message":"something went wrong"`) {
		t.Errorf("expected message in %s", result)
	}
}
