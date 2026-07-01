package repomap

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// extractGo pulls package-level declarations from Go source via go/parser.
// Unparseable files yield nil (skip, don't abort the build).
func extractGo(src []byte) []Symbol {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil || file == nil {
		return nil
	}

	var syms []Symbol
	line := func(pos token.Pos) int { return fset.Position(pos).Line }

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := "func"
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
				name = recvType(d.Recv.List[0].Type) + "." + name
			}
			syms = append(syms, Symbol{Name: name, Kind: kind, Line: line(d.Pos())})

		case *ast.GenDecl:
			kind := genKind(d.Tok)
			if kind == "" {
				continue
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					syms = append(syms, Symbol{Name: s.Name.Name, Kind: "type", Line: line(s.Pos())})
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == "_" {
							continue
						}
						syms = append(syms, Symbol{Name: n.Name, Kind: kind, Line: line(n.Pos())})
					}
				}
			}
		}
	}
	return syms
}

// genKind maps a GenDecl token to a symbol kind ("" to skip imports).
func genKind(tok token.Token) string {
	switch tok {
	case token.CONST:
		return "const"
	case token.VAR:
		return "var"
	case token.TYPE:
		return "type"
	default:
		return ""
	}
}

// recvType renders a method receiver type name, stripping pointers.
func recvType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver Foo[T]
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	default:
		return ""
	}
}
