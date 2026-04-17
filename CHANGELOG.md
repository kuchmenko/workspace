# Changelog

## [0.6.0](https://github.com/kuchmenko/workspace/compare/v0.5.0...v0.6.0) (2026-04-17)


### Features

* **docs:** add ws docs --agent for AI agent capability discovery ([ab13faf](https://github.com/kuchmenko/workspace/commit/ab13faff65b97af48c6ccdcd368d8d97729f626b))
* **docs:** add ws docs --agent for AI agent capability discovery ([0112c96](https://github.com/kuchmenko/workspace/commit/0112c962678ada42d7f714d0bc981d597bd0d75e))
* **doctor:** add ws doctor diagnostic command ([89bad6a](https://github.com/kuchmenko/workspace/commit/89bad6a67fbb735852a7e2f939a9b6b2d4fae780))
* **doctor:** add ws doctor diagnostic command ([f31a4e1](https://github.com/kuchmenko/workspace/commit/f31a4e1b4d52e5fabfbe52acbe8ac081608262ea)), closes [#15](https://github.com/kuchmenko/workspace/issues/15)
* **doctor:** stream per-scope output so progress is visible during checks ([a4b3c90](https://github.com/kuchmenko/workspace/commit/a4b3c9071610e9fa32c1061b4426e50a33c01940))


### Bug Fixes

* **doctor:** fetch after setting fetch-refspec so branch-upstream converges ([1163a39](https://github.com/kuchmenko/workspace/commit/1163a39da8d3248ff42a73336fa1798a6d8255da))
* **doctor:** move post-fix fetch into branch-upstream where the ref is needed ([a27eabc](https://github.com/kuchmenko/workspace/commit/a27eabce01b64fc702c0e83ba97d0daa4a5d7af6))
* **git:** install remote.origin.fetch refspec in bare repos ([5d928f4](https://github.com/kuchmenko/workspace/commit/5d928f4025fbab82a55e53c0685608439e53a178))
* **git:** install remote.origin.fetch refspec in bare repos ([432e193](https://github.com/kuchmenko/workspace/commit/432e193adbd395aac6cd092e6218523bbed0be94)), closes [#14](https://github.com/kuchmenko/workspace/issues/14)

## [0.5.0](https://github.com/kuchmenko/workspace/compare/v0.4.0...v0.5.0) (2026-04-14)


### Features

* **agent:** context-sensitive two-line footer with all available shortcuts ([41e45cd](https://github.com/kuchmenko/workspace/commit/41e45cde3a040285b4c4cbd2163e9d0b97742f92))
* **agent:** inline y/n confirmation before worktree delete ([92d94f5](https://github.com/kuchmenko/workspace/commit/92d94f5654fe4045ac3c556b7b0d012980112bf7))
* **agent:** session/worktree caches, delete guards, promote autopush ([b01071b](https://github.com/kuchmenko/workspace/commit/b01071bd3c48b6f3a26debfa091a16a436f03a3c))
* **agent:** session/worktree caches, delete guards, promote autopush, visual polish ([9f3b096](https://github.com/kuchmenko/workspace/commit/9f3b096212d81159480ae07b4850c101472f538b))
* **agent:** ws root walk-up detection + ws agent resume subcommand ([34618e8](https://github.com/kuchmenko/workspace/commit/34618e8cea60496f3126425c1f7de0d100f37e29))
* **worktree:** auto-detect existing remote branch ([54ace90](https://github.com/kuchmenko/workspace/commit/54ace90ee2dee9afa25e12140927b291e9555167))
* **worktree:** auto-detect existing remote branch in ws worktree new ([03ac804](https://github.com/kuchmenko/workspace/commit/03ac8042852c69433951910ce02bf320347af386)), closes [#8](https://github.com/kuchmenko/workspace/issues/8)


### Bug Fixes

* **agent:** address review — path-based workspace lookup, safe promote ordering ([4e52079](https://github.com/kuchmenko/workspace/commit/4e52079f3ce3608d133fa8f0a8328f7466dfa133))

## [0.4.0](https://github.com/kuchmenko/workspace/compare/v0.3.0...v0.4.0) (2026-04-10)


### Features

* **agent:** canvas TUI for launching Claude Code across workspaces ([0151c05](https://github.com/kuchmenko/workspace/commit/0151c0543a22f46d27205eb10806c47469e959d5))
* **agent:** context-sensitive toolbar + claude on groups ([fa75aa3](https://github.com/kuchmenko/workspace/commit/fa75aa3bc4b510346c3ad7288e513c04d426f08d))
* **agent:** flash labels inline like flash.nvim ([6343f9b](https://github.com/kuchmenko/workspace/commit/6343f9bc7a3e283b7983cde9ac13b6fcb7d2a480))
* **agent:** flash search with jump labels (s or /) ([85037f3](https://github.com/kuchmenko/workspace/commit/85037f3a57e4f62cecfb70f548e0c104d742d75d))
* **agent:** l/→ opens shell in any item's directory ([a176384](https://github.com/kuchmenko/workspace/commit/a1763848bad95b21433cf08ef0fee61c4ecc6924))
* **agent:** launcher + bare ws entry point ([070261d](https://github.com/kuchmenko/workspace/commit/070261dd7695ef6019817809865c01fcca293b63))
* **agent:** open shell in project/worktree directory ([e84bdbd](https://github.com/kuchmenko/workspace/commit/e84bdbdbef33aec1064caf4c049533d52aa7247b))
* **agent:** promote branch from TUI ([352d8d8](https://github.com/kuchmenko/workspace/commit/352d8d8c94ef6b8545f094bec2d4d9db6c083442))
* **agent:** q-as-back, prompt input, CLI subcommands ([44700b3](https://github.com/kuchmenko/workspace/commit/44700b33bc9bb38c49720e58d92cc453e4153295))
* **agent:** sessions parser, worktree listing, session badges ([3f805c9](https://github.com/kuchmenko/workspace/commit/3f805c9efe3d040172fe953e6deae25cfc4477b9))
* **agent:** warm amber redesign + which-key + smart flash search ([0983100](https://github.com/kuchmenko/workspace/commit/09831003e6ce4775d48a4c7cc7d01c9d0f66f3b3))
* **agent:** worktree creation form with branch + auto-push ([2bcacf8](https://github.com/kuchmenko/workspace/commit/2bcacf87e6ea055aa014d7d52bc8eaa755104a9c))
* **agent:** worktree management — create-only, delete, display names ([c152b3a](https://github.com/kuchmenko/workspace/commit/c152b3aee099e1b19be6b024aeb2ff93b79a046a))
* **pulse:** cross-machine activity dashboard with PRs and inbox tabs ([c2836fa](https://github.com/kuchmenko/workspace/commit/c2836fa9bb4be672c12e522bc261130bf0d0ba20))


### Bug Fixes

* **agent:** derive worktree path from branch when branch is explicit ([3fcea8f](https://github.com/kuchmenko/workspace/commit/3fcea8f9b34fb5016b7a78ac51bdf969acc270be))
* **agent:** new worktree + session now goes through prompt input ([86b684a](https://github.com/kuchmenko/workspace/commit/86b684a9b87b4a1b1627d17adf6aaec9be776dde))
* **agent:** nil guard on pendingLaunch in viewPromptInput ([bca59a8](https://github.com/kuchmenko/workspace/commit/bca59a8bf55c824162aa8dce0b2b1f7746bccb9f))
* **agent:** promote now moves worktree directory + renames branch ([9c6d158](https://github.com/kuchmenko/workspace/commit/9c6d15865c0b3bf58a373a195ce563f42820877e))
* **worktree:** derive path from branch in ws worktree new --branch ([e1c29bc](https://github.com/kuchmenko/workspace/commit/e1c29bcedc18f67457985572123bfa755121d003))


### Performance Improvements

* **agent:** optimize graphics renderer — text cache, shm double-buffer, benchmarks ([3e8771a](https://github.com/kuchmenko/workspace/commit/3e8771a39b00ba8cb80e15b0e7f4f361378ca75d))

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
