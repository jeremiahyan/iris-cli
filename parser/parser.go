package parser

import (
	"fmt"
	"go/ast"
	"go/parser"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	// "go/printer"
	"go/token"
	// 	"go/types"
	// "go/importer"

	"github.com/kataras/iris-cli/utils"
)

// AssetDir represents the parsed asset directories that are handled by Iris.
type AssetDir struct {
	Dir             string
	ShouldGenerated bool
}

type SourceCommand struct {
	Dir  string   // working dir, the filename dir.
	Name string   // the command to run.
	Args []string // any command's arguments.
}

// Result is the `Parse` return value.
type Result struct {
	AssetDirs []*AssetDir
	Commands  []*SourceCommand
}

const dirOptionsDeclName = "iris.DirOptions"

// Parse accepts a source and returns a `Result`.
// Source "src" can be a filepath, directory or Go source code contents.
func Parse(src interface{}) (*Result, error) {
	res := new(Result)

	var (
		input    []byte
		filename string
	)

	fset := token.NewFileSet()
	if s, ok := src.(string); ok {
		if utils.Exists(s) {
			// it's a file or dir.
			if utils.IsDir(s) {
				// return fmt.Errorf("path <%s> is not a go file", src)
				pkgs, err := parser.ParseDir(fset, s, nil, parser.ParseComments)
				if err != nil {
					return nil, err
				}

				for _, pkg := range pkgs {
					for filename, node := range pkg.Files {
						if filepath.Base(filename) == "bindata.go" {
							continue // skip go bindata generated file.
						}
						parseFile(node, filename, res)
					}
				}

				return res, nil
			}

			filename = s
			b, err := ioutil.ReadFile(s)
			if err != nil {
				return nil, err
			}

			input = b

		} else {
			input = []byte(s)
		}
	} else if b, ok := src.([]byte); ok {
		input = b
	} else {
		return nil, fmt.Errorf("unknown source <%v>", src)
	}

	node, err := parser.ParseFile(fset, filename, input, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	parseFile(node, filename, res)

	return res, nil
}

func parseFile(node *ast.File, filename string, res *Result) {
	for _, comment := range node.Comments {
		commentText := strings.TrimSpace(strings.TrimSuffix(comment.Text(), "\n"))
		if !strings.HasPrefix(commentText, "$") {
			continue
		}
		commands := strings.Split(commentText, "$")
		for _, command := range commands {
			command = strings.TrimSpace(command)
			if command == "" {
				continue
			}

			args := strings.Split(command, " ")
			name := args[0]
			if len(args) > 1 {
				args = args[1:]
			} else {
				args = args[0:0]
			}

			cmd := &SourceCommand{
				Dir:  filepath.Dir(filename),
				Name: name,
				Args: args,
			}
			res.Commands = append(res.Commands, cmd)
		}
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if expr, ok := n.(*ast.ExprStmt); ok {
			if c, ok := expr.X.(*ast.CallExpr); ok {
				if cSelector, ok := c.Fun.(*ast.SelectorExpr); ok {
					if cSelector.Sel.Name != "HandleDir" {
						return true // we only care for ".HandleDir"
					}
				}

				isDirOptsGoBindata := false
				if nargs := len(c.Args); nargs >= 2 {
					if nargs == 3 {
						// contains the last optional iris.DirOptions argument.
						if dirOpts, ok := c.Args[2].(*ast.CompositeLit); ok {
							if dirOptSel, ok := dirOpts.Type.(*ast.SelectorExpr); ok {
								pkgF := ""
								if s, ok := dirOptSel.X.(*ast.Ident); ok {
									pkgF = s.Name
								}

								if pkgF+"."+dirOptSel.Sel.Name == dirOptionsDeclName {
									// should be iris.DirOptions, no other package name.
									for _, eltExpr := range dirOpts.Elts {
										if kvExpr, ok := eltExpr.(*ast.KeyValueExpr); ok {
											if key, ok := kvExpr.Key.(*ast.Ident); ok {
												if key.Name == "Asset" {
													if value, ok := kvExpr.Value.(*ast.Ident); ok {
														if value.Name == "Asset" {
															// iris.DirOptions{ Asset: Asset...}
															// Probably, should be generated by go-bindata.
															isDirOptsGoBindata = true
														}
													}
												}
											}

										}
									}
								}

							}
						}
					}

					assetsDir := ""

					// now get the previous arg[1]; first is the request path and second(!) is the go-bindata target path we want to extract.
					switch arg := c.Args[1].(type) {
					case *ast.BasicLit:
						assetsDir = arg.Value
						// cmd.Printf("go-bindata target is (basic lit): %s\n",  assetsDir)
					case *ast.Ident:
						searchVar := arg.Name
						for _, decl := range node.Decls {
							if gen, ok := decl.(*ast.GenDecl); ok {
								for _, spec := range gen.Specs {
									if spec, ok := spec.(*ast.ValueSpec); ok {
										for _, id := range spec.Names {
											if searchVar == id.Name {
												// found the varialbe, get its value.
												assetsDir = id.Obj.Decl.(*ast.ValueSpec).Values[0].(*ast.BasicLit).Value
												// cmd.Printf("go-bindata target is (variable): %s\n", assetsDir)
											}
										}
									}
								}

							}
						}
					}

					s, err := strconv.Unquote(assetsDir)
					if err == nil {
						assetsDir = s
					}
					res.AssetDirs = append(res.AssetDirs, &AssetDir{Dir: assetsDir, ShouldGenerated: isDirOptsGoBindata})
				}
			}
		}
		return true
	})
}
