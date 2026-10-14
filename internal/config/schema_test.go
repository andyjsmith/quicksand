package config

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The JSON Schema (for editors) must describe exactly the fields Config
// accepts, and the example config must follow it.

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../config.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// resolve follows a "#/definitions/..." reference.
func resolve(root, node map[string]any) map[string]any {
	if ref, ok := node["$ref"].(string); ok {
		return root["definitions"].(map[string]any)[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)
	}
	return node
}

func TestSchemaMatchesConfig(t *testing.T) {
	root := loadSchema(t)
	var check func(path string, typ reflect.Type, node map[string]any)
	check = func(path string, typ reflect.Type, node map[string]any) {
		node = resolve(root, node)
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			if typ.Kind() == reflect.Slice {
				node = resolve(root, node["items"].(map[string]any))
			}
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || typ == reflect.TypeFor[Duration]() {
			return
		}
		props, _ := node["properties"].(map[string]any)
		var fields []string
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields = append(fields, name)
			sub, ok := props[name].(map[string]any)
			if !ok {
				t.Errorf("schema lacks %s%s", path, name)
				continue
			}
			check(path+name+".", f.Type, sub)
		}
		for name := range props {
			if name != "$schema" && !slices.Contains(fields, name) {
				t.Errorf("schema has %s%s, which the config doesn't", path, name)
			}
		}
	}
	check("", reflect.TypeFor[Config](), root)
}

func TestExampleFollowsSchema(t *testing.T) {
	root := loadSchema(t)
	b, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var example any
	if err := json.Unmarshal(b, &example); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any, node map[string]any)
	walk = func(path string, v any, node map[string]any) {
		node = resolve(root, node)
		switch v := v.(type) {
		case map[string]any:
			props, _ := node["properties"].(map[string]any)
			for k, sub := range v {
				schema, ok := props[k].(map[string]any)
				if !ok {
					t.Errorf("config.example.json: %s%s isn't in the schema", path, k)
					continue
				}
				walk(path+k+".", sub, schema)
			}
		case []any:
			for _, item := range v {
				walk(path, item, node["items"].(map[string]any))
			}
		}
	}
	walk("", example, root)
}
