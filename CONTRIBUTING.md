# Contributing

Issues and pull requests are welcome.

- Describe the problem with your YouLess model and firmware (`curl http://<youless>/d`),
  and what you expected. Leave out your MAC address and the P1 telegram.
- Code: Go, standard library only, formatted with `gofmt`. Every change comes
  with tests; run `sh tools/verify.sh` and, for anything around the stack,
  `sh tools/integration.sh` ([docs/development.md](docs/development.md)).
- Behaviour of the device (paging, formats) is pinned by recordings in
  `testdata/ls120`. A change there needs a new recording that shows it.
- Note user-visible changes under *Unreleased* in `CHANGELOG.md`.

By contributing you agree that your contribution is licensed under the GPL-3.0.
