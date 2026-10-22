package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// appSrc is the package that declares the status codes and actions.
const appSrc = "../app"

// appConstants returns the string values of the constants declared with
// type typeName in the app package's non-test sources.
func appConstants(t *testing.T, typeName string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(appSrc, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var values []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != typeName {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s: %s constant with a non-literal value", path, typeName)
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					values = append(values, s)
				}
			}
		}
	}
	sort.Strings(values)
	return values
}

// TestAppCodesAndActionsHaveLocaleKeys checks that every status code the
// app can report has a title and detail, and every action a button label,
// so a new code or action cannot show its raw key in the window or tray.
func TestAppCodesAndActionsHaveLocaleKeys(t *testing.T) {
	en, err := loadLocale("en")
	if err != nil {
		t.Fatalf("loadLocale(en) error = %v", err)
	}
	codes := appConstants(t, "Code")
	actions := appConstants(t, "Action")
	if len(codes) == 0 || len(actions) == 0 {
		t.Fatalf("found %d codes and %d actions in %s; is the parser still finding them?", len(codes), len(actions), appSrc)
	}
	var keys []string
	for _, c := range codes {
		keys = append(keys, "status."+c+".title", "status."+c+".detail")
	}
	for _, a := range actions {
		keys = append(keys, "action."+a)
	}
	for _, key := range keys {
		if _, ok := en[key]; !ok {
			t.Errorf("locales/en.json has no %q", key)
		}
	}
}
