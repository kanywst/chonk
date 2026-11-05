# Changelog

## [0.2.0](https://github.com/kanywst/chonk/compare/v0.1.0...v0.2.0) (2026-11-05)


### Features

* record a snapshot every day via a launchd agent (schedule on) ([#3](https://github.com/kanywst/chonk/issues/3)) ([711923f](https://github.com/kanywst/chonk/commit/711923ffe590e266287116cb2d9c05fd96561018))

## 0.1.0 (2026-11-04)


### Features

* chonk, a disk view grouped by owner that reclaims with each owner's own command ([fd01fdb](https://github.com/kanywst/chonk/commit/fd01fdb87baa0e7b8b9a7c2e92e1542129b05014))
* **demo:** canned machine and a recorded demo.gif ([9162372](https://github.com/kanywst/chonk/commit/9162372d01a34ade5bb620cce39a886f4e53aec0))
* tie processes to owners, and show snapshots and space outside the scan ([4752869](https://github.com/kanywst/chonk/commit/47528699e36bc326433c80b59d0fe407aa376db5))


### Bug fixes

* close the gaps a review found ([05d00a3](https://github.com/kanywst/chonk/commit/05d00a3389edf8607bde7e0e36ee4e2c6c230425))
* **tui:** no running-process warning for a trim, which removes nothing ([23faf92](https://github.com/kanywst/chonk/commit/23faf92ec6a1666549e47523cd33549a43329f77))


### Performance

* **walk:** read directories with getattrlistbulk on macOS ([7a3e9c8](https://github.com/kanywst/chonk/commit/7a3e9c814906aab6c3852bfde4d973b937edcf85))
