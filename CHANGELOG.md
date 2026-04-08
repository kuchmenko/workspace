# Changelog

## [0.3.0](https://github.com/kuchmenko/workspace/compare/v0.2.1...v0.3.0) (2026-04-08)


### Features

* **bootstrap:** add ws bootstrap TUI and daemon auto-clone ([90aa93a](https://github.com/kuchmenko/workspace/commit/90aa93ad9e3a70ee5836751b82ad332b67af01d0))
* **cli:** teach scan/status/archive about the worktree layout ([dd36bdb](https://github.com/kuchmenko/workspace/commit/dd36bdb8b9d4903f1c9dec30edf098be08139102))
* **clone,migrate:** set upstream on default branch via direct config ([ab3ff3e](https://github.com/kuchmenko/workspace/commit/ab3ff3ea41da7696b6e9299909052d92939f4c3a))
* **conflict:** add ws sync resolve and notify-send wiring ([25f182a](https://github.com/kuchmenko/workspace/commit/25f182ad7a8d659bdf0a886699f8f8135797d5c2))
* **daemon:** replace syncer/poller with unified reconciler ([50d23b1](https://github.com/kuchmenko/workspace/commit/50d23b1ad846227e5eef5c028c5a158a355faf9c))
* **git,config:** add worktree/bare helpers and machine config ([1616a54](https://github.com/kuchmenko/workspace/commit/1616a54d77123439eac955e701cb95177a5d7f92))
* **migrate:** add ws migrate with verify-before-delete and WIP snapshots ([d055a7d](https://github.com/kuchmenko/workspace/commit/d055a7d42ac8e9ff49cecbed13a54c0d8008733b))
* **migrate:** TUI default + worktree-attach rewrite + sidecar + tests + CI ([3e2feeb](https://github.com/kuchmenko/workspace/commit/3e2feebb81a24365b11c22733fd90729f9f93f7b))
* **worktree:** add promote command and --branch/--auto-push flags ([0b280bb](https://github.com/kuchmenko/workspace/commit/0b280bb2c32132ee61eee725e49637391f15b74b))
* **worktree:** add ws worktree new/list/rm commands ([316013f](https://github.com/kuchmenko/workspace/commit/316013f68ac391231aa222ccbe17697d257bb8b2))


### Bug Fixes

* **migrate:** populate index from HEAD + use clean tmp parent for admin dir name ([f931364](https://github.com/kuchmenko/workspace/commit/f931364fb044142c40c542e221f340a20190e76d))

## [0.2.1](https://github.com/kuchmenko/workspace/compare/v0.2.0...v0.2.1) (2026-04-07)


### Features

* **alias:** add shell aliases for projects, groups, and workspace root ([996be82](https://github.com/kuchmenko/workspace/commit/996be825d0f867e0d7cafeab955963ca794a235a))
* **alias:** render TUI as tree (root → groups → projects) ([5791b6f](https://github.com/kuchmenko/workspace/commit/5791b6f1082821dde65277e18804c8e35a082659))
