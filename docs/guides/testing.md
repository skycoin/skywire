# Testing

Before pushing commits to a pull request, it's customary in case of
edits to any of the golang source code to run the following:

```
make format check
```

`make check` will run `make test` as well. To explicitly run tests, use
`make test`.
