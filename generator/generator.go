package generator

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"strings"
)

func GenerateFake(
	structName, packageName string,
	interfaceNode *ast.InterfaceType) (string, error) {
	gen := generator{
		structName:    structName,
		packageName:   packageName,
		interfaceNode: interfaceNode,
	}

	buf := new(bytes.Buffer)
	err := printer.Fprint(buf, token.NewFileSet(), gen.SourceFile())
	return buf.String(), err
}

type generator struct {
	structName    string
	packageName   string
	interfaceNode *ast.InterfaceType
}

func (gen *generator) SourceFile() ast.Node {
	return &ast.File{
		Name: &ast.Ident{Name: gen.packageName},
		Decls: append([]ast.Decl{
			gen.imports(),
			gen.typeDecl(),
			gen.constructorDecl(),
		}, gen.methodDecls()...),
	}
}

func (gen *generator) imports() ast.Decl {
	return &ast.GenDecl{
		Tok: token.IMPORT,
		Specs: []ast.Spec{&ast.ImportSpec{
			Path: &ast.BasicLit{
				Kind:  token.STRING,
				Value: `"sync"`,
			},
		}},
	}
}

func (gen *generator) typeDecl() ast.Decl {
	return &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: &ast.Ident{Name: gen.structName},
				Type: &ast.StructType{
					Fields: &ast.FieldList{
						List: gen.structFields(),
					},
				},
			},
		},
	}
}

func (gen *generator) constructorDecl() ast.Decl {
	name := ast.NewIdent("New" + gen.structName)
	return &ast.FuncDecl{
		Name: name,
		Type: &ast.FuncType{
			Results: &ast.FieldList{
				List: []*ast.Field{
					{
						Type: &ast.StarExpr{X: ast.NewIdent(gen.structName)},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.UnaryExpr{
							Op: token.AND,
							X: &ast.CompositeLit{
								Type: ast.NewIdent(gen.structName),
								Elts: []ast.Expr{},
							},
						},
					},
				},
			},
		},
	}
}

func (gen *generator) methodDecls() []ast.Decl {
	result := []ast.Decl{}
	for _, method := range gen.interfaceNode.Methods.List {
		result = append(
			result,
			gen.methodImplementation(method),
			gen.callsListGetter(method),
		)
	}
	return result
}

func (gen *generator) structFields() []*ast.Field {
	result := []*ast.Field{
		{
			Type: &ast.SelectorExpr{
				X:   ast.NewIdent("sync"),
				Sel: ast.NewIdent("RWMutex"),
			},
		},
	}

	for _, method := range gen.interfaceNode.Methods.List {
		result = append(
			result,

			&ast.Field{
				Names: []*ast.Ident{methodImplFuncIdent(method)},
				Type:  method.Type,
			},

			&ast.Field{
				Names: []*ast.Ident{callsListFieldIdent(method)},
				Type: &ast.ArrayType{
					Elt: gen.argsStructTypeForMethod(method),
				},
			})
	}

	return result
}

func (gen *generator) argsStructTypeForMethod(method *ast.Field) *ast.StructType {
	methodType := method.Type.(*ast.FuncType)

	paramFields := []*ast.Field{}
	for _, field := range methodType.Params.List {
		paramFields = append(paramFields, &ast.Field{
			Type:  field.Type,
			Names: []*ast.Ident{ast.NewIdent(publicize(nameForMethodParam(field)))},
		})
	}

	return &ast.StructType{
		Fields: &ast.FieldList{List: paramFields},
	}
}

func nameForMethodParam(param *ast.Field) string {
	if len(param.Names) > 0 {
		return param.Names[0].Name
	} else {
		panic("Don't handle anonymous args yet!")
	}
}

func (gen *generator) methodImplementation(method *ast.Field) *ast.FuncDecl {
	methodType := method.Type.(*ast.FuncType)

	forwardArgs := []ast.Expr{}
	for _, field := range methodType.Params.List {
		forwardArgs = append(forwardArgs, ast.NewIdent(nameForMethodParam(field)))
	}

	forwardCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   receiverIdent(),
			Sel: methodImplFuncIdent(method),
		},
		Args: forwardArgs,
	}

	var callStatement ast.Stmt
	if methodType.Results != nil {
		callStatement = &ast.ReturnStmt{
			Results: []ast.Expr{forwardCall},
		}
	} else {
		callStatement = &ast.ExprStmt{
			X: forwardCall,
		}
	}

	return &ast.FuncDecl{
		Name: method.Names[0],
		Type: methodType,
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{receiverIdent()},
					Type:  &ast.StarExpr{X: ast.NewIdent(gen.structName)},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   receiverIdent(),
							Sel: ast.NewIdent("Lock"),
						},
					},
				},
				&ast.DeferStmt{
					Call: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   receiverIdent(),
							Sel: ast.NewIdent("Unlock"),
						},
					},
				},
				&ast.AssignStmt{
					Tok: token.ASSIGN,
					Lhs: []ast.Expr{&ast.SelectorExpr{
						X:   receiverIdent(),
						Sel: callsListFieldIdent(method),
					}},
					Rhs: []ast.Expr{&ast.CallExpr{
						Fun: ast.NewIdent("append"),
						Args: []ast.Expr{
							&ast.SelectorExpr{
								X:   receiverIdent(),
								Sel: callsListFieldIdent(method),
							},
							&ast.CompositeLit{
								Type: gen.argsStructTypeForMethod(method),
								Elts: forwardArgs,
							},
						},
					}},
				},
				callStatement,
			},
		},
	}
}

func (gen *generator) callsListGetter(method *ast.Field) *ast.FuncDecl {
	return &ast.FuncDecl{
		Name: callsListMethodIdent(method),
		Type: &ast.FuncType{
			Results: &ast.FieldList{List: []*ast.Field{
				&ast.Field{
					Type: &ast.ArrayType{
						Elt: gen.argsStructTypeForMethod(method),
					},
				},
			}},
		},
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{receiverIdent()},
					Type:  &ast.StarExpr{X: ast.NewIdent(gen.structName)},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   receiverIdent(),
							Sel: ast.NewIdent("RLock"),
						},
					},
				},
				&ast.DeferStmt{
					Call: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   receiverIdent(),
							Sel: ast.NewIdent("RUnlock"),
						},
					},
				},
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.SelectorExpr{
							X:   receiverIdent(),
							Sel: callsListFieldIdent(method),
						},
					},
				},
			},
		},
	}
}

func receiverIdent() *ast.Ident {
	return ast.NewIdent("fake")
}

func callsListMethodIdent(method *ast.Field) *ast.Ident {
	return ast.NewIdent(method.Names[0].Name + "Calls")
}

func callsListFieldIdent(method *ast.Field) *ast.Ident {
	return ast.NewIdent(privatize(callsListMethodIdent(method).Name))
}

func methodImplFuncIdent(method *ast.Field) *ast.Ident {
	return ast.NewIdent(method.Names[0].Name + "Stub")
}

func publicize(input string) string {
	return strings.ToUpper(input[0:1]) + input[1:]
}

func privatize(input string) string {
	return strings.ToLower(input[0:1]) + input[1:]
}