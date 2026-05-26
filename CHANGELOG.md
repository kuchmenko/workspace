# Changelog

## [0.7.0](https://github.com/kuchmenko/workspace/compare/v0.6.0...v0.7.0) (2026-05-26)


### ⚠ BREAKING CHANGES

* **worktree:** user-typed branches, no daemon auto-push of projects
* `ws add <url>` no longer creates a plain checkout. The on-disk shape is now `personal/<name>.bare/` + `personal/<name>/` worktree, identical to what `ws bootstrap` produces. Tooling that specifically expected `personal/<name>/.git/` as a real directory (rather than a worktree pointer file) needs to handle the worktree case.
* `ws group` CLI subcommands no longer exist. Edit workspace.toml directly or use `ws setup` to manage groups.
* `ws list` no longer exists. Use `ws status` (with `--filter` if you need a subset).
* `ws archive`, `ws restore`, and `ws clean` no longer exist. Use `git clean -xfd` or rm -rf for dep caches; worktree-aware archive support is deferred to a future rewrite if ever needed.
* `ws pulse` no longer exists. No replacement planned.

### Features

* **add:** bubbletea TUI for `ws add` interactive flow ([0b8eba6](https://github.com/kuchmenko/workspace/commit/0b8eba67e43e1a1053e32adccb61575e092fdd60))
* **add:** cache GitHub repos + stream gather sources progressively ([45c42eb](https://github.com/kuchmenko/workspace/commit/45c42eb5edefaaa8319ae1567a820037ffcf4e7f))
* **add:** internal/add core with sidecar coordination ([18a9b61](https://github.com/kuchmenko/workspace/commit/18a9b61c2cf05f106730f3c752e4e474f364de7d))
* **add:** multi-select bulk add in ws add TUI ([3444a75](https://github.com/kuchmenko/workspace/commit/3444a75553ff067e9bb8eb7d490eb53a85d7cd93))
* **add:** real source implementations — disk, clipboard, github ([0071290](https://github.com/kuchmenko/workspace/commit/0071290746d14c7721770bba26c3ae617428711d))
* **add:** show repo description on selected row + search across description ([da95294](https://github.com/kuchmenko/workspace/commit/da9529449b78a455041b783aaaa03ce01175f55a))
* **add:** tree view by org + highlight already-cloned suggestions ([60452d2](https://github.com/kuchmenko/workspace/commit/60452d2d2dc58643a42d9a0f793214b7dc9bed67))
* **add:** wire add.Run into the live TUI ([731bbb6](https://github.com/kuchmenko/workspace/commit/731bbb6cdf56e9cbf73acd29fac1c2bb63865ec5))
* **agent:** add favorites and recent-projects quick-nav header ([#46](https://github.com/kuchmenko/workspace/issues/46)) ([32b9032](https://github.com/kuchmenko/workspace/commit/32b9032c9b2526619c838faba76a2e305b5bba24))
* **agent:** edit project group/category from ws agent TUI ([f52ee42](https://github.com/kuchmenko/workspace/commit/f52ee42ca2abade741b36274ff5e5c3169180496))
* **config:** introduce BranchMeta schema with legacy autopush migration ([facb2d8](https://github.com/kuchmenko/workspace/commit/facb2d88cc0c78883beb4be377a825f77eff999e))
* **create:** bootstrap GitHub repos via gh + register + clone in one shot ([4edd025](https://github.com/kuchmenko/workspace/commit/4edd02568b6e5b2a8e5fe4c20a761bde7b881490))
* **create:** bootstrap GitHub repos via gh + register + clone in one shot ([80b5735](https://github.com/kuchmenko/workspace/commit/80b573556cc430a58bf92fe4477e3535cf533258))
* **daemon:** coalesce workspace.toml auto-sync commits via push cooldown ([#47](https://github.com/kuchmenko/workspace/issues/47)) ([730b8e7](https://github.com/kuchmenko/workspace/commit/730b8e769de2ebe6e4079979a947bde3b739367f))
* edit project metadata + bulk add in TUI ([47f01ac](https://github.com/kuchmenko/workspace/commit/47f01ac48f97cc20d7fe7edd360ebc62e1d71ecc))
* **github:** add Provider interface for suggestion sources ([9d2a009](https://github.com/kuchmenko/workspace/commit/9d2a0092ac9e1c431290f809b8922e54d88ae129))
* **path:** resolve project name to absolute path for shell substitution ([b491f3c](https://github.com/kuchmenko/workspace/commit/b491f3cc08439301fbadee114dd150bacae39b8b))
* **path:** resolve project name to absolute path for shell substitution ([7ff4056](https://github.com/kuchmenko/workspace/commit/7ff40563295f4f34c66324fc33cfd8afcf7ad39c)), closes [#28](https://github.com/kuchmenko/workspace/issues/28)
* remove ws archive / restore / clean ([c383e09](https://github.com/kuchmenko/workspace/commit/c383e099eb344927f0abf1539cc19736ee03c43c))
* remove ws group CLI subcommands ([34a5857](https://github.com/kuchmenko/workspace/commit/34a58578baa1c17b025592051d6ff01043011703))
* remove ws list ([e3d304d](https://github.com/kuchmenko/workspace/commit/e3d304d5fb903fe0cf73a6177eae84954913dbe0))
* remove ws pulse command ([0c5de41](https://github.com/kuchmenko/workspace/commit/0c5de41e5eeba5f53617ff5c1121e7cea8710ae6))
* **worktree:** user-typed branches, no daemon auto-push of projects ([644a471](https://github.com/kuchmenko/workspace/commit/644a471921920edef45357aa1501b22402c29323))
* ws add now clones as bare+worktree ([28f1095](https://github.com/kuchmenko/workspace/commit/28f1095ecb433e820f3d933afe7344883dc55037))


### Bug Fixes

* **add:** align cursor index with rendered tree order; highlight selected row ([aa6e83b](https://github.com/kuchmenko/workspace/commit/aa6e83b90f473fc86c74f6fe4f29cebabedc02c7))
* **add:** raise gather timeout to 10s, surface error reason in chip ([45bd31f](https://github.com/kuchmenko/workspace/commit/45bd31faf1929f6526579211b23cadcc250a1cf7))
* **agent:** mirror CLI's fetch-then-attach flow in TUI worktree create ([27886fd](https://github.com/kuchmenko/workspace/commit/27886fdcdd518e59a4eb3e7ca2e943cf446ba9a6))
* **github,add:** probe OAuth token, fall back to gh CLI on 401; clearer chip hints ([120ccd2](https://github.com/kuchmenko/workspace/commit/120ccd26efd1255a1684474d3f8846e9d7d5a296))
* **github:** reject malformed cache entries on read ([05fc4e1](https://github.com/kuchmenko/workspace/commit/05fc4e1cc979ba6d46436275688de5583d48eb38))
* **reconciler:** use last_pushed_at for orphan detection, not last_active_at ([bac394f](https://github.com/kuchmenko/workspace/commit/bac394f6f0fe552720a1a71963b0cc9347ea3846))
* **worktree:** re-register existing checkouts; release branch on TUI delete ([8759322](https://github.com/kuchmenko/workspace/commit/875932232d9637ef4cd4f112908a7cd0501cbfd0))
* **worktree:** refuse to remove main worktree by branch ([5a987d9](https://github.com/kuchmenko/workspace/commit/5a987d9642d2fb053571ac83933ce58400803f58))
* **worktree:** repair fetch refspec on add; allow dropping orphan without worktree ([7e4844b](https://github.com/kuchmenko/workspace/commit/7e4844b57c726353ea51531d33c65a53ea0203de))
* **worktree:** stop force-fetch over local branches; persist keep-local ([310d74d](https://github.com/kuchmenko/workspace/commit/310d74d942c8e1b6703da9ad301066b4c11c72a7))

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
