// Package archtest enforces the plan's module rules: who may import whom, and
// who may write to which table. Constitution principle II states the rules, and
// this test keeps them true.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/maltehedderich/mailarkiv"

// allowedImports copies the plan's "Allowed imports" line. A module that isn't
// listed fails the check, so a new one needs a decision before it can land.
var allowedImports = map[string][]string{
	"cmd/mailarkiv": {
		"frontend",
		"internal/archive",
		"internal/fault",
		"internal/identity",
		"internal/opstatus",
		"internal/sqlitedb",
		"internal/web",
	},
	"internal/web":      {"internal/archive", "internal/identity", "internal/opstatus", "internal/fault"},
	"internal/archive":  {"internal/opstatus", "internal/sqlitedb", "internal/fault"},
	"internal/identity": {"internal/sqlitedb"},
	"internal/opstatus": {},
	"internal/sqlitedb": {},
	"internal/fault":    {},
}

// tableOwners copies the data model's table ownership.
var tableOwners = map[string]string{
	"account":            "internal/identity",
	"session":            "internal/identity",
	"sign_in_failure":    "internal/identity",
	"sign_in_block":      "internal/identity",
	"message":            "internal/archive",
	"source_observation": "internal/archive",
}

// tableWrite finds the table a SQL write names. It ignores case, so a stray
// "update message" in plain text fails loudly and gets reworded, instead of
// lowercase SQL passing unnoticed. A word after the verb that isn't a table,
// such as the SET in DO UPDATE SET, is ignored below.
var tableWrite = regexp.MustCompile(
	`(?i)\b(?:INSERT(?:\s+OR\s+\w+)?\s+INTO|REPLACE\s+INTO|UPDATE(?:\s+OR\s+\w+)?|DELETE\s+FROM)` +
		`\s+(?:\w+\.)?["` + "`" + `\[]?(\w+)`)

// checkFile returns one line per broken rule in a parsed file of module.
func checkFile(module string, file *ast.File) []string {
	var problems []string

	allowed, known := allowedImports[module]
	if !known {
		problems = append(problems, fmt.Sprintf("%s has no import rule", module))
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s has an unreadable import %s", module, spec.Path.Value))
			continue
		}
		target, inRepository := strings.CutPrefix(path, modulePath+"/")
		if inRepository && !slices.Contains(allowed, target) {
			problems = append(problems, fmt.Sprintf("%s imports %s", module, target))
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		literal, isLiteral := node.(*ast.BasicLit)
		if !isLiteral || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s has an unreadable string %s", module, literal.Value))
			return true
		}
		for _, match := range tableWrite.FindAllStringSubmatch(text, -1) {
			table := strings.ToLower(match[1])
			if owner, owned := tableOwners[table]; owned && owner != module {
				problems = append(problems, fmt.Sprintf("%s writes to %s, which %s owns", module, table, owner))
			}
		}
		return true
	})

	return problems
}

// TestModuleRules checks the real tree. go/parser ignores build tags, so the
// files behind -tags faults are checked too.
func TestModuleRules(t *testing.T) {
	root := filepath.Join("..", "..")
	visited := map[string]bool{}

	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			module := filepath.ToSlash(relative)
			visited[module] = true

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			for _, problem := range checkFile(module, file) {
				t.Errorf("%s: %s", path, problem)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// A walk from the wrong directory would find nothing and pass.
	for _, module := range []string{"cmd/mailarkiv", "internal/web", "internal/sqlitedb", "internal/opstatus"} {
		if !visited[module] {
			t.Errorf("the walk didn't reach %s, only %v", module, visited)
		}
	}
}

func parseSource(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "source.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %q: %v", source, err)
	}
	return file
}

func importing(path string) string {
	return fmt.Sprintf("package x\nimport _ %q\n", modulePath+"/"+path)
}

func usingSQL(sql string) string {
	return fmt.Sprintf("package x\nconst query = %s\n", "`"+sql+"`")
}

// Each row breaks one import rule, so the checker can't pass on an empty tree.
func TestCheckerCatchesEachImportRule(t *testing.T) {
	tests := []struct {
		module string
		target string
	}{
		{"internal/web", "internal/sqlitedb"},
		{"internal/web", "frontend"},
		{"internal/archive", "internal/identity"},
		{"internal/archive", "internal/web"},
		{"internal/archive", "frontend"},
		{"internal/identity", "internal/archive"},
		{"internal/identity", "internal/opstatus"},
		{"internal/identity", "internal/web"},
		{"internal/identity", "internal/fault"},
		{"internal/identity", "frontend"},
		{"internal/opstatus", "internal/sqlitedb"},
		{"internal/opstatus", "internal/fault"},
		{"internal/sqlitedb", "internal/opstatus"},
		{"internal/sqlitedb", "internal/fault"},
		{"internal/fault", "internal/opstatus"},
		{"internal/fault", "internal/sqlitedb"},
	}
	for _, tt := range tests {
		t.Run(tt.module+" imports "+tt.target, func(t *testing.T) {
			problems := checkFile(tt.module, parseSource(t, importing(tt.target)))
			if len(problems) != 1 {
				t.Fatalf("problems = %v, want exactly one", problems)
			}
		})
	}
}

func TestCheckerRefusesAModuleWithNoRule(t *testing.T) {
	problems := checkFile("internal/newmodule", parseSource(t, "package newmodule\n"))
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
}

// The other half of the rule. A checker that flags everything would also pass
// the test above.
func TestCheckerAllowsTheDeclaredImports(t *testing.T) {
	tests := []struct {
		module string
		target string
	}{
		{"internal/web", "internal/archive"},
		{"internal/web", "internal/identity"},
		{"internal/web", "internal/opstatus"},
		{"internal/web", "internal/fault"},
		{"internal/archive", "internal/opstatus"},
		{"internal/archive", "internal/sqlitedb"},
		{"internal/archive", "internal/fault"},
		{"internal/identity", "internal/sqlitedb"},
		{"cmd/mailarkiv", "frontend"},
		{"cmd/mailarkiv", "internal/web"},
		{"cmd/mailarkiv", "internal/identity"},
		{"cmd/mailarkiv", "internal/archive"},
		{"cmd/mailarkiv", "internal/sqlitedb"},
		{"cmd/mailarkiv", "internal/opstatus"},
	}
	for _, tt := range tests {
		t.Run(tt.module+" imports "+tt.target, func(t *testing.T) {
			if problems := checkFile(tt.module, parseSource(t, importing(tt.target))); len(problems) != 0 {
				t.Fatalf("problems = %v, want none", problems)
			}
		})
	}

	t.Run("outside this repository", func(t *testing.T) {
		source := "package x\nimport _ \"modernc.org/sqlite\"\nimport _ \"net/http\"\n"
		if problems := checkFile("internal/fault", parseSource(t, source)); len(problems) != 0 {
			t.Fatalf("problems = %v, want none", problems)
		}
	})
}

func TestCheckerCatchesAWriteToEachTable(t *testing.T) {
	statements := []string{
		"INSERT INTO %s (a) VALUES (1)",
		"UPDATE %s SET a = 1",
		"DELETE FROM %s WHERE a = 1",
	}
	for table, owner := range tableOwners {
		for _, statement := range statements {
			sql := fmt.Sprintf(statement, table)
			for _, module := range []string{"internal/web", "cmd/mailarkiv", "internal/sqlitedb", otherOwner(owner)} {
				t.Run(module+" "+sql, func(t *testing.T) {
					problems := checkFile(module, parseSource(t, usingSQL(sql)))
					if len(problems) != 1 {
						t.Fatalf("problems = %v, want exactly one", problems)
					}
				})
			}
			t.Run(owner+" "+sql, func(t *testing.T) {
				if problems := checkFile(owner, parseSource(t, usingSQL(sql))); len(problems) != 0 {
					t.Fatalf("problems = %v, want none", problems)
				}
			})
		}
	}
}

func otherOwner(owner string) string {
	if owner == "internal/identity" {
		return "internal/archive"
	}
	return "internal/identity"
}

func TestCheckerFindsWritesInOtherForms(t *testing.T) {
	tests := []string{
		"insert into message (a) values (1)",
		"INSERT OR IGNORE INTO message (a) VALUES (1)",
		"INSERT OR REPLACE INTO message (a) VALUES (1)",
		"REPLACE INTO message (a) VALUES (1)",
		"UPDATE OR ABORT message SET a = 1",
		"INSERT INTO main.message (a) VALUES (1)",
		`INSERT INTO "message" (a) VALUES (1)`,
		"INSERT INTO [message] (a) VALUES (1)",
		"Update\n\t\tmessage\n\t\tSET a = 1",
	}
	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			source := fmt.Sprintf("package x\nconst query = %q\n", sql)
			if problems := checkFile("internal/web", parseSource(t, source)); len(problems) != 1 {
				t.Fatalf("problems = %v, want exactly one", problems)
			}
		})
	}
}

func TestCheckerIgnoresWhatIsNotAWrite(t *testing.T) {
	tests := []string{
		"SELECT id FROM message",
		"SELECT m.id FROM message m JOIN source_observation o ON o.id = m.latest_observation_id",
		"update the catalog",
		"please delete from the list",
		"INSERT INTO unowned_table (a) VALUES (1) ON CONFLICT(a) DO UPDATE SET a = 2",
		"message",
	}
	for _, sql := range tests {
		t.Run(sql, func(t *testing.T) {
			if problems := checkFile("internal/web", parseSource(t, usingSQL(sql))); len(problems) != 0 {
				t.Fatalf("problems = %v, want none", problems)
			}
		})
	}
}
