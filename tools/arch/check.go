// Package arch checks the source-level privilege and registration boundaries.
package arch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Check returns all architecture violations under root. Process and privilege
// checks include tests; registration checks apply only to production sources.
// Testdata is excluded; fixture modules are checked via their own root.
func Check(root string) []string {
	var problems []string
	names := map[string]string{}
	files := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == filepath.Join(root, dir) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, e := parser.ParseFile(fset, path, nil, 0)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(root, path)
			files[filepath.ToSlash(rel)] = file
			return nil
		})
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	report := func(path, msg string) { problems = append(problems, path+": "+msg) }
	references := map[string]int{}
	for path, file := range files {
		if !strings.HasPrefix(path, "internal/verbs/") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, e := range lit.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || (key.Name != "Run" && key.Name != "Exec") {
						continue
					}
					if id, ok := kv.Value.(*ast.Ident); ok {
						references[filepath.Dir(path)+"/"+id.Name]++
					}
				}
			}
			return true
		})
	}
	for path, file := range files {
		inExec := strings.HasPrefix(path, "internal/exec/")
		client := strings.HasPrefix(path, "cmd/panel-web/") || strings.HasPrefix(path, "cmd/novapanelctl/")
		imports := map[string]string{}
		for _, im := range file.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			alias := filepath.Base(p)
			if im.Name != nil {
				alias = im.Name.Name
			}
			imports[alias] = p
			if !inExec && (p == "os/exec" || p == "syscall") {
				report(path, "process import outside internal/exec: "+p)
			}
			if client && (p == "novapanel/internal/exec" || p == "novapanel/internal/store" || p == "novapanel/internal/secret" || p == "golang.org/x/sys/unix") {
				report(path, "privileged client import: "+p)
			}
		}
		registered := map[*ast.CompositeLit]bool{}
		// Handler references are counted across the entire verbs package.
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !inExec {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok {
						pkg := imports[id.Name]
						if (pkg == "os" && sel.Sel.Name == "StartProcess") || (pkg == "golang.org/x/sys/unix" && (sel.Sel.Name == "Exec" || sel.Sel.Name == "ForkExec")) {
							report(path, "process spawn outside internal/exec")
						}
					}
				}
			}
			// Any shell token inside a process invocation is forbidden, including
			// literals nested in argv slices and absolute shell paths.
			method := ""
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				method = f.Sel.Name
			case *ast.Ident:
				method = f.Name
			}
			if method == "Run" || method == "Command" || method == "CommandContext" || method == "StartProcess" || method == "Exec" || method == "ForkExec" {
				for _, arg := range call.Args {
					ast.Inspect(arg, func(n ast.Node) bool {
						if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, _ := strconv.Unquote(lit.Value)
							base := filepath.Base(s)
							if base == "sh" || base == "bash" || base == "dash" || base == "zsh" || s == "-c" {
								report(path, "shell execution token: "+s)
							}
						}
						return true
					})
				}
			}
			if !strings.HasPrefix(path, "internal/verbs/") || strings.HasSuffix(path, "_test.go") {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Register" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || imports[id.Name] != "novapanel/internal/rpc" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.CompositeLit)
				if !ok {
					report(path, "Register requires a literal Verb declaration")
					continue
				}
				registered[lit] = true
				name := ""
				handler := false
				for _, e := range lit.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch key.Name {
					case "Name":
						if s, ok := kv.Value.(*ast.BasicLit); ok {
							name, _ = strconv.Unquote(s.Value)
						}
					case "Run", "Exec":
						if id, ok := kv.Value.(*ast.Ident); !ok || id.Name != "nil" {
							handler = true
						}
						// Counted in the package-wide pass above.
					}
				}
				if name == "" {
					report(path, "registered verb has empty or nonliteral Name")
				} else if previous, ok := names[name]; ok {
					report(path, fmt.Sprintf("duplicate registered verb %q (also %s)", name, previous))
				} else {
					names[name] = path
				}
				if !handler {
					report(path, "registered verb lacks Run/Exec handler")
				}
			}
			return true
		})
		if strings.HasPrefix(path, "internal/verbs/") && !strings.HasSuffix(path, "_test.go") {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || registered[lit] {
					return true
				}
				for _, e := range lit.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok && (k.Name == "Run" || k.Name == "Exec") {
							report(path, "unregistered verb handler declaration")
						}
					}
				}
				return true
			})
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name == "init" || fn.Type.Params == nil {
					continue
				}
				handler := false
				for _, p := range fn.Type.Params.List {
					if star, ok := p.Type.(*ast.StarExpr); ok {
						if s, ok := star.X.(*ast.SelectorExpr); ok && s.Sel.Name == "Agent" {
							handler = true
						}
					}
				}
				if handler && references[filepath.Dir(path)+"/"+fn.Name.Name] != 1 {
					report(path, "unregistered handler or multiply registered handler: "+fn.Name.Name)
				}
			}
		}
	}
	// Boot behavior itself is tested in cmd/panel-web. This source check catches
	// deletion of the injectable privilege check, even if tests were removed too.
	if file, ok := files["cmd/panel-web/main.go"]; ok {
		hasGeteuid, hasRefusal := false, false
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if n.Sel.Name == "Geteuid" {
					hasGeteuid = true
				}
			case *ast.IfStmt:
				usesUID := false
				ast.Inspect(n.Cond, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && id.Name == "geteuid" {
						usesUID = true
					}
					return true
				})
				if usesUID {
					ast.Inspect(n.Body, func(n ast.Node) bool {
						if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) > 0 {
							hasRefusal = true
						}
						if c, ok := n.(*ast.CallExpr); ok {
							if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Exit" {
								hasRefusal = true
							}
						}
						return true
					})
				}
			}
			return true
		})
		if !hasGeteuid || !hasRefusal {
			report("cmd/panel-web/main.go", "missing root refusal using injectable geteuid")
		}
	}
	return problems
}
