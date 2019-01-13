/*symbol-check

this program checks for incompatible symbols(extra exported symbols and incompatible type definitions) that might break forward compatibility when built as a plugin.

Discussion at https://github.com/gotify/server/issues/51#issuecomment-452954279

Sample usage:
$ go run github.com/gotify/plugin-api/cmd/symbol-check > export_ref_do_not_edit.json # take a snapshot of the current export in every major release
$ go run github.com/gotify/plugin-api/cmd/symbol-check -c export_ref_do_not_edit.json # compare current version for incompatible definitions
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"strings"
)

var workDir string
var compareTo string
var pkgName string

type SymbolList []Symbol

func compareSymbolList(source, target SymbolList, cmpLabel bool) []string {
	diffs := make([]string, 0)

	agg := make(map[string]*Symbol)
	for _, symbol := range source {
		sym := symbol
		agg[symbol.Ident()] = &sym
	}
	for _, symbol := range target {
		if origSymbol, ok := agg[symbol.Ident()]; ok {
			agg[symbol.Ident()] = nil
			diffs = append(diffs, compareSymbol(*origSymbol, symbol, cmpLabel)...)
		} else {
			diffs = append(diffs, fmt.Sprintf("extra symbol found: %s", symbol))
		}
	}
	for _, symbol := range agg {
		if symbol != nil {
			diffs = append(diffs, fmt.Sprintf("missing symbol: %s", symbol))
		}
	}

	return diffs
}

type Symbol struct {
	Label          string     `json:"label,omitempty"`
	SymbolType     string     `json:"type"`
	UnderlyingType string     `json:"underlyingType,omitempty"`
	ReceiverType   string     `json:"receiverType,omitempty"`
	FileName       string     `json:"fileName,omitempty"`
	Pos            token.Pos  `json:"pos,omitempty"`
	Members        SymbolList `json:"members,omitempty"`
	FuncSpec       *FuncSpec  `json:"funcSpec,omitempty"`
}

func (c Symbol) Ident() string {
	return fmt.Sprintf("%s.%s", c.ReceiverType, c.Label)
}

func (c Symbol) String() string {
	res := c.Ident()
	if c.FileName != "" && c.Pos != 0 {
		res += fmt.Sprintf(" (%s:offset %d)", c.FileName, c.Pos)
	}
	return res
}

func compareSymbol(a, b Symbol, cmpLabel bool) []string {
	diffs := make([]string, 0)

	if a.SymbolType != b.SymbolType {
		diffs = append(diffs, fmt.Sprintf("%s and %s have different symbol types: %s and %s", a, b, a.SymbolType, b.SymbolType))
	}
	if cmpLabel && a.Label != b.Label {
		diffs = append(diffs, fmt.Sprintf("%s and %s have different labels: %s and %s", a, b, a.Label, b.Label))

	}
	if a.SymbolType == "type" && a.UnderlyingType != b.UnderlyingType {
		diffs = append(diffs, fmt.Sprintf("type alias %s and %s have different underlying types: %s and %s", a, b, a.UnderlyingType, b.UnderlyingType))
	}
	if a.SymbolType == "method" && a.ReceiverType != b.ReceiverType {
		diffs = append(diffs, fmt.Sprintf("method %s and %s have different receiver types: %s and %s", a, b, a.ReceiverType, b.ReceiverType))
	}
	diffs = append(diffs, compareSymbolList(a.Members, b.Members, true)...)
	if a.SymbolType == "func" {
		diffs = append(diffs, compareFuncSpec(*a.FuncSpec, *b.FuncSpec)...)
	}

	return diffs
}

type FuncSpec struct {
	Params  SymbolList `json:"params,omitempty"`
	Returns SymbolList `json:"returns,omitempty"`
}

func compareFuncSpec(a, b FuncSpec) []string {
	diffs := make([]string, 0)
	for _, diff := range compareSymbolList(a.Params, b.Params, false) {
		diffs = append(diffs, "func param mismatch: "+diff)
	}
	for _, diff := range compareSymbolList(a.Returns, b.Returns, false) {
		diffs = append(diffs, "func result mismatch: "+diff)
	}
	return diffs
}

func exitWithStatusString(s string, code int) {
	fmt.Fprintln(os.Stderr, s)
	os.Exit(code)
}

func exitWithStatusError(err error, code int) {
	exitWithStatusString(err.Error(), code)
}

func init() {
	workDirFlag := flag.String("d", "./", "work dir")
	compareToFlag := flag.String("c", "", "compare to")
	pkgNameFlag := flag.String("p", "", "package name - can be omitted if only one package exists")
	flag.Parse()
	workDir = *workDirFlag
	compareTo = *compareToFlag
	pkgName = *pkgNameFlag
}

func main() {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, workDir, nil, 0)
	if err != nil {
		exitWithStatusError(err, 1)
	}
	if pkgName == "" {
		if len(pkgs) == 1 {
			for pName := range pkgs {
				pkgName = pName
			}
		} else {
			panic("multiple packages found")
		}
	}
	pkg := pkgs[pkgName]
	files := make([]*ast.File, 0)
	for _, file := range pkg.Files {
		files = append(files, file)
	}

	exports := make(SymbolList, 0)
	for fileName, file := range pkg.Files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if !decl.Name.IsExported() {
					break
				}
				if decl.Recv == nil {
					exports = append(exports, Symbol{
						Label:      decl.Name.Name,
						SymbolType: "func",
						FileName:   fileName,
						Pos:        decl.Pos() - file.Pos(),
						FuncSpec:   funcSpec(decl.Type),
					})
				} else {
					exports = append(exports, Symbol{
						Label:        decl.Name.Name,
						SymbolType:   "method",
						ReceiverType: findReceiver(decl),
						FileName:     fileName,
						Pos:          decl.Pos() - file.Pos(),
						FuncSpec:     funcSpec(decl.Type),
					})
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if !ast.IsExported(spec.Name.Name) {
							break
						}
						res := formatType(spec, file.Pos())
						res.FileName = fileName
						exports = append(exports, *res)
					case *ast.ValueSpec:
						if !ast.IsExported(spec.Names[0].Name) {
							break
						}
						exports = append(exports, Symbol{
							Label:      spec.Names[0].Name,
							SymbolType: "var",
							FileName:   fileName,
							Pos:        spec.Pos() - file.Pos(),
						})
					}
				}
			}
		}
	}
	if compareTo != "" {
		refDataBytes, err := ioutil.ReadFile(compareTo)
		if err != nil {
			panic(err)
		}
		refData := new(SymbolList)
		if err := json.Unmarshal(refDataBytes, refData); err != nil {
			panic(err)
		}
		if diff := compareSymbolList(*refData, exports, true); len(diff) > 0 {
			fmt.Fprintln(os.Stderr, strings.Join(diff, "\r\n"))
			exitWithStatusString("symbols are not compatible", 2)
		} else {
			exitWithStatusString("symbols are compatible", 0)
		}
	} else {
		resultJSON, err := json.Marshal(&exports)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(resultJSON))
	}
}

func findReceiver(decl *ast.FuncDecl) string {
	for _, field := range decl.Recv.List {
		if typ, ok := field.Type.(*ast.Ident); ok {
			return typ.Name
		}
	}
	return "unknown"
}

func funcSpec(decl *ast.FuncType) *FuncSpec {
	res := FuncSpec{}

	if decl.Params != nil {
		for _, param := range decl.Params.List {
			//fmt.Printf("%T %s\n", param.Type, formatType(param.Type))
			typ := &ast.TypeSpec{
				Type: param.Type,
			}
			res.Params = append(res.Params, *formatType(typ, 0))
		}
	}
	if decl.Results != nil {
		for _, result := range decl.Results.List {
			typ := &ast.TypeSpec{
				Type: result.Type,
			}
			res.Returns = append(res.Returns, *formatType(typ, 0))
		}
	}

	return &res
}

func formatType(spec *ast.TypeSpec, basePos token.Pos) *Symbol {
	switch specType := spec.Type.(type) {
	case *ast.InterfaceType:
		members := make(SymbolList, 0)
		for _, methodDecl := range specType.Methods.List {
			if len(methodDecl.Names) == 0 {
				members = append(members, Symbol{
					Label:      methodDecl.Type.(*ast.Ident).String(),
					SymbolType: "embed",
				})
			} else {
				members = append(members, Symbol{
					Label:      methodDecl.Names[0].Name,
					SymbolType: "method",
					FuncSpec:   funcSpec(methodDecl.Type.(*ast.FuncType)),
				})
			}
		}
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		res := &Symbol{
			Label:      name,
			SymbolType: "interface",
			Members:    members,
		}
		if basePos != 0 {
			res.Pos = spec.Pos() - basePos
		}
		return res
	case *ast.StructType:
		members := make(SymbolList, 0)
		for _, methodDecl := range specType.Fields.List {
			if len(methodDecl.Names) == 0 {
				members = append(members, Symbol{
					Label:      methodDecl.Type.(*ast.Ident).String(),
					SymbolType: "embed",
				})
			} else {
				members = append(members, Symbol{
					Label:      methodDecl.Names[0].Name,
					SymbolType: "member",
				})
			}
		}
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		}
		res := &Symbol{
			Label:      name,
			SymbolType: "struct",
			Members:    members,
		}
		if basePos != 0 {
			res.Pos = spec.Pos() - basePos
		}
		return res
	case *ast.Ident:
		res := &Symbol{
			SymbolType:     "type",
			UnderlyingType: specType.Name,
		}
		if spec.Name != nil {
			res.Label = spec.Name.Name
		}
		if basePos != 0 {
			res.Pos = spec.Pos() - basePos
		}
		return res
	case *ast.ArrayType:
		res := &Symbol{
			Label:      "[]" + fmt.Sprint(specType.Elt),
			SymbolType: "array",
		}
		if basePos != 0 {
			res.Pos = spec.Pos() - basePos
		}
		return res
	default:
		panic("unknown type")
	}
}
