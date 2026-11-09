package compatibility_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update-api", false, "write the reviewed public API baseline")

// TestPublicAPI checks source compatibility without importing internal packages.
// Additions are allowed; existing exported declarations, fields and methods must
// retain their types. Struct literals must be keyed, as documented for v1.
func TestPublicAPI(t *testing.T) {
	root := filepath.Join("..", "..")
	cmd := exec.Command("go", "list", "-deps", "-export", "-json", "./decision", "./metrics", "./middleware", "./operation", "./postgres", "./priority", "./regulator", "./sqlguard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	exports := map[string]string{}
	paths := []string{}
	for {
		var p struct {
			ImportPath, Export string
			DepOnly            bool
		}
		err := decoder.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
		if !p.DepOnly {
			paths = append(paths, p.ImportPath)
		}
	}
	imp := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) })
	api := map[string]string{}
	// Canonicalize aliases recursively in printed signatures. Go 1.22 and later
	// toolchains expose aliases differently, although the source API is identical.
	aliases := map[string]string{}
	for _, path := range paths {
		pkg, err := imp.Import(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range pkg.Scope().Names() {
			if v, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && v.IsAlias() {
				aliases[path+"."+name] = types.TypeString(types.Unalias(v.Type()), func(p *types.Package) string { return p.Path() })
			}
		}
	}
	keys := []string{}
	for key := range aliases {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	typeString := func(v types.Type) string {
		result := types.TypeString(v, func(p *types.Package) string { return p.Path() })
		for _, key := range keys {
			result = strings.ReplaceAll(result, key, aliases[key])
		}
		return result
	}
	for _, path := range paths {
		pkg, err := imp.Import(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range pkg.Scope().Names() {
			obj := pkg.Scope().Lookup(name)
			if !obj.Exported() {
				continue
			}
			key := path + "." + name
			switch v := obj.(type) {
			case *types.Const:
				api[key] = "const " + typeString(v.Type()) + " = " + v.Val().ExactString()
			case *types.Func:
				api[key] = typeString(v.Type())
			case *types.Var:
				api[key] = "var " + typeString(v.Type())
			case *types.TypeName:
				typ := types.Unalias(v.Type())
				if v.IsAlias() {
					api[key] = "alias " + typeString(typ)
				} else {
					api[key] = "type"
				}
				switch u := typ.Underlying().(type) {
				case *types.Struct:
					if !v.IsAlias() {
						api[key] = "struct"
					}
					for i := 0; i < u.NumFields(); i++ {
						f := u.Field(i)
						if f.Exported() {
							api[key+".field."+f.Name()] = fmt.Sprintf("%s embedded=%t tag=%q", typeString(f.Type()), f.Embedded(), u.Tag(i))
						}
					}
				case *types.Interface:
					api[key] = "interface " + typeString(u.Complete())
				default:
					if !v.IsAlias() {
						api[key] = "type " + typeString(u)
					}
				}
				for _, variant := range []struct {
					name string
					typ  types.Type
				}{{"value", typ}, {"pointer", types.NewPointer(typ)}} {
					methods := types.NewMethodSet(variant.typ)
					for i := 0; i < methods.Len(); i++ {
						m := methods.At(i)
						if m.Obj().Exported() {
							api[key+"."+variant.name+"."+m.Obj().Name()] = typeString(m.Type())
						}
					}
				}
			}
		}
	}
	file := filepath.Join("testdata", "v1-api.json")
	if *update {
		b, _ := json.MarshalIndent(api, "", "  ")
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]string
	if err = json.Unmarshal(b, &baseline); err != nil {
		t.Fatal(err)
	}
	for key, want := range baseline {
		if got, ok := api[key]; !ok || got != want {
			t.Errorf("public API changed: %s\nwas: %s\nnow: %s", key, want, got)
		}
	}
	t.Logf("checked %d frozen API entries across %d public packages", len(baseline), len(paths))
}
