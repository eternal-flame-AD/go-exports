# go-exports

Go exported symbol analyzer

Golang poses a [strict](https://github.com/gotify/server/issues/51#issuecomment-452954279) requirement on interoperability of packages imported by both plugins and the main package. This util generates a package outline consisting of: Exported symbols, types, funcs, struct members and interface methods.

To generate a spec:
```bash
$ go run github.com/eternal-flame-AD/go-exports > export_ref_do_not_edit.json # take a snapshot of the current export in every major release
```
To compare current code to a spec:
```bash
$ go run github.com/eternal-flame-AD/go-exports -c export_ref_do_not_edit.json
```