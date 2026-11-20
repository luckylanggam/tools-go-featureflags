package validate_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/validate"
)

// FuzzValidate feeds any pair of documents that parse to Validate: it must
// not panic, must give the same answer twice, and every problem must say
// where and what. Seeds are the document package's fixtures.
func FuzzValidate(f *testing.F) {
	flagFiles, _ := filepath.Glob(filepath.Join("..", "document", "testdata", "flags", "*", "*.json"))
	catalogFiles, _ := filepath.Glob(filepath.Join("..", "document", "testdata", "catalog", "*", "*.json"))
	catalogFiles = append(catalogFiles, "")
	for _, ff := range flagFiles {
		flags, err := os.ReadFile(ff)
		if err != nil {
			f.Fatal(err)
		}
		for _, cf := range catalogFiles {
			catalog := []byte(catalogJSON)
			if cf != "" {
				if catalog, err = os.ReadFile(cf); err != nil {
					f.Fatal(err)
				}
			}
			f.Add(flags, catalog)
		}
	}

	f.Fuzz(func(t *testing.T, flagsData, catalogData []byte) {
		flags, err := document.ParseFlags(flagsData)
		if err != nil {
			return
		}
		catalog, err := document.ParseCatalog(catalogData)
		if err != nil {
			return
		}

		warnings, err := validate.Validate(flags, catalog)
		warnings2, err2 := validate.Validate(flags, catalog)
		if !reflect.DeepEqual(warnings, warnings2) || !reflect.DeepEqual(err, err2) {
			t.Fatalf("not deterministic:\n%v %v\n%v %v", warnings, err, warnings2, err2)
		}
		for _, w := range warnings {
			checkProblem(t, w)
		}
		if err == nil {
			return
		}
		verr, ok := err.(*validate.Error)
		if !ok || len(verr.Problems) == 0 {
			t.Fatalf("error is not a non-empty *validate.Error: %v", err)
		}
		for _, p := range verr.Problems {
			checkProblem(t, p)
		}
	})
}

func checkProblem(t *testing.T, p validate.Problem) {
	t.Helper()
	if p.Path == "" || p.Message == "" {
		t.Fatalf("problem without path or message: %#v", p)
	}
}
