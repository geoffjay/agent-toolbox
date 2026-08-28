package anthropicmodel

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

// TestToolInputSchemaShape guards against the doubly-nested input_schema
// bug: the SDK's Properties field holds only the property map, so the
// whole parameters schema must be split across Properties/Required rather
// than assigned wholesale. Anthropic validates against JSON Schema draft
// 2020-12 and rejects the nested form with a 400.
func TestToolInputSchemaShape(t *testing.T) {
	fn := &genai.FunctionDeclaration{
		Name: "read_file",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"path": {Type: genai.TypeString, Description: "repo-relative path"},
			},
			Required: []string{"path"},
		},
	}
	tp, err := convertFunctionDeclaration(fn)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalToMap(t, tp.InputSchema)
	if got["type"] != "object" {
		t.Errorf("type = %v, want object", got["type"])
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not an object: %v", got["properties"])
	}
	if _, ok := props["path"].(map[string]any); !ok {
		t.Errorf("properties.path is not a schema object: %#v", props["path"])
	}
	if _, nested := props["type"]; nested {
		t.Errorf("properties incorrectly contains top-level schema keys: %#v", props)
	}
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "path" {
		t.Errorf("required = %#v, want [\"path\"]", got["required"])
	}
}

// TestToolInputSchemaNoParams verifies a parameterless tool still produces
// a valid object schema with an (empty) properties object.
func TestToolInputSchemaNoParams(t *testing.T) {
	fn := &genai.FunctionDeclaration{
		Name:       "pr_files",
		Parameters: &genai.Schema{Type: genai.TypeObject},
	}
	tp, err := convertFunctionDeclaration(fn)
	if err != nil {
		t.Fatal(err)
	}
	got := marshalToMap(t, tp.InputSchema)
	if got["type"] != "object" {
		t.Errorf("type = %v, want object", got["type"])
	}
	if _, ok := got["properties"].(map[string]any); !ok {
		t.Errorf("properties should be an object, got %#v", got["properties"])
	}
}

// TestFunctionCallInputIsObject guards against the base64 bug: assigning
// marshaled bytes to the any-typed tool_use Input field would encode them
// as a base64 string, which the API rejects with
// "tool_use.input: Input should be an object".
func TestFunctionCallInputIsObject(t *testing.T) {
	block := convertFunctionCallToBlock(&genai.FunctionCall{
		ID:   "call_1",
		Name: "read_file",
		Args: map[string]any{"path": "main.go", "max_lines": 250},
	})
	got := marshalToMap(t, block.OfToolUse)
	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_use.input is not a JSON object: %#v (type %T)", got["input"], got["input"])
	}
	if input["path"] != "main.go" {
		t.Errorf("input.path = %v, want main.go", input["path"])
	}
}

// TestFunctionCallInputNilArgs verifies nil args serialize as an empty
// object, never null.
func TestFunctionCallInputNilArgs(t *testing.T) {
	block := convertFunctionCallToBlock(&genai.FunctionCall{ID: "c", Name: "pr_files"})
	got := marshalToMap(t, block.OfToolUse)
	if _, ok := got["input"].(map[string]any); !ok {
		t.Errorf("nil args should marshal as empty object, got %#v", got["input"])
	}
}

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}
