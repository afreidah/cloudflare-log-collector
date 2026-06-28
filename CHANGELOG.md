# Changelog

All notable changes to this project are documented in this file.


## [0.1.20](https://github.com/afreidah/cloudflare-log-collector/compare/v0.1.19...v0.1.20) (2026-06-28)


### Bug Fixes

* **ci:** lock govulncheck via go.mod tool dependency (S8545) ([#57](https://github.com/afreidah/cloudflare-log-collector/issues/57)) ([210989d](https://github.com/afreidah/cloudflare-log-collector/commit/210989d09dfcc313054b9576ca6c3799fa026bd8))

## [0.1.19](https://github.com/afreidah/cloudflare-log-collector/compare/v0.1.18...v0.1.19) (2026-06-28)


### Bug Fixes

* **ci:** resolve SonarCloud Dockerfile and workflow findings ([#53](https://github.com/afreidah/cloudflare-log-collector/issues/53)) ([ef43092](https://github.com/afreidah/cloudflare-log-collector/commit/ef43092c70f3f0f734ed977d57a3657f0265a069))

## [0.1.18](https://github.com/afreidah/cloudflare-log-collector/compare/v0.1.17...v0.1.18) (2026-06-28)


### Features

* add RUM / Web Analytics collector (page views + Core Web Vitals) ([#51](https://github.com/afreidah/cloudflare-log-collector/issues/51)) ([e29a9b9](https://github.com/afreidah/cloudflare-log-collector/commit/e29a9b9ec2c0d525b9f73d9dd2533558205eb045))

## [0.1.17](https://github.com/afreidah/cloudflare-log-collector/compare/v0.1.16...v0.1.17) (2026-06-23)


### Bug Fixes

* align audit-log collector with logging/error-handling/tracing patterns ([703d671](https://github.com/afreidah/cloudflare-log-collector/commit/703d671e8a4ae9eb0f9694d8df6889fa270f767b)), closes [#38](https://github.com/afreidah/cloudflare-log-collector/issues/38)
* align audit-log collector with logging/error-handling/tracing patterns ([#39](https://github.com/afreidah/cloudflare-log-collector/issues/39)) ([162e3a4](https://github.com/afreidah/cloudflare-log-collector/commit/162e3a4096cf35d6ab599d3fff9d7fb1c2088e79))

## [0.1.16](https://github.com/afreidah/cloudflare-log-collector/compare/v0.1.15...v0.1.16) (2026-06-01)


### Features

* account audit logs + release-please (adapted from [#33](https://github.com/afreidah/cloudflare-log-collector/issues/33)) ([#35](https://github.com/afreidah/cloudflare-log-collector/issues/35)) ([b26114b](https://github.com/afreidah/cloudflare-log-collector/commit/b26114ba4c9e9a97dde25a40f94c74d6499efb9d))
* add account audit logs ingest ([d0f10f1](https://github.com/afreidah/cloudflare-log-collector/commit/d0f10f195b90d84998971ad078600160ee4b1992))


### Bug Fixes

* **deps:** clear govulncheck findings ([a17b7c4](https://github.com/afreidah/cloudflare-log-collector/commit/a17b7c49cda0edf69d87200fe074068fb956bc00))

## [0.1.15] - 2026-03-16

### Added
- Add Go API reference to documentation site (#24)
- Add auto-generated Go API reference to documentation site
- Add Debian packaging, GoReleaser, and Aptly publishing (#22)
- Add Debian packaging, GoReleaser, Aptly publishing, and boost test coverage

### Fixed
- Fix import grouping and boost test coverage (#20)
- Fix import grouping and boost test coverage to 65%

### Improved
- update CHANGELOG.md for v0.1.12 (#19)

## [0.1.12] - 2026-03-16

### Fixed
- Fix service graph visibility in Tempo (#18)
- Fix service graph visibility in Tempo by using CLIENT span kind

### Improved
- update CHANGELOG.md for v0.1.11 (#16)

### Other
- Move logo above title in README and reorder header elements
- added logo to readme

## [0.1.11] - 2026-03-15

### Added
- Add Hugo documentation site (#13)
- Add Hugo documentation site

### Improved
- update CHANGELOG.md for v0.1.10 (#11)

### Other
- Polish documentation site: landing cards, page headers, logo sizing (#15)
- Polish documentation site: landing cards, page headers, logo sizing

## [0.1.10] - 2026-03-15

### Improved
- update CHANGELOG.md for v0.1.9 (#10)

### Other
- general repo housekeeping/setup

## [0.1.8] - 2026-03-15

### Added
- add multi-zone support

### Fixed
- fix timing rejection issue (#7)
- fix timing rejection issue
- Fix reliability issues and improve Go best practices (#5) (#6)
- Fix reliability issues and improve Go best practices (#5)

### Improved
- updated image

### Other
- setting up release functionality for repo to match other go projects ... (#8)
- setting up release functionality for repo to match other go projects I have
- dashboard fix
- Feat: added grafana dashboard and some code changes to make the dashboardd better.  put an image of it on the readme
- Ship HTTP traffic to Loki, add country metrics, CI/CD and project docs
- Initial commit: Cloudflare analytics collector
