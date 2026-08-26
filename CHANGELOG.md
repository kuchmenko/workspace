# Changelog

## [0.12.0](https://github.com/kuchmenko/workspace/compare/v0.11.0...v0.12.0) (2026-08-26)


### Features

* **explorer:** manage Amp runners and aliases ([030b455](https://github.com/kuchmenko/workspace/commit/030b455625d0ddb58d8ac644bb388fe9bcc9caa5))
* **explorer:** manage Amp runners and aliases ([d0124ec](https://github.com/kuchmenko/workspace/commit/d0124ec8f1044ab7eb34ce3c9d441eea7b1670fa))
* **runners:** edit persisted runner definitions ([c73419e](https://github.com/kuchmenko/workspace/commit/c73419ea112dad8ce8a5738fdc90441bb7474d64))

## [0.11.0](https://github.com/kuchmenko/workspace/compare/v0.10.0...v0.11.0) (2026-08-19)


### Features

* add trusted P2P workspace synchronization ([e17e907](https://github.com/kuchmenko/workspace/commit/e17e907485c291cc6463bbb7aff578c42ff604b7))
* **cli:** manage and sync shared workspaces ([901b105](https://github.com/kuchmenko/workspace/commit/901b105f1a4205befc74b42e1ab42234ea4c4bcc))
* **network:** add trusted device pairing ([37dbbb8](https://github.com/kuchmenko/workspace/commit/37dbbb87a09a115a4e4f0832353b3cb99957beb9))
* **network:** exchange authorized workspace revisions ([f517532](https://github.com/kuchmenko/workspace/commit/f51753232d2fdbf7ba59615e18a570e4b47a1779))
* **network:** replicate signed membership events ([6d63e87](https://github.com/kuchmenko/workspace/commit/6d63e87e459433968c87915fc8a09d319219e576))
* **registry:** add signed peer workspace history ([2b06e30](https://github.com/kuchmenko/workspace/commit/2b06e301a98074282f8cf8e213b8266ec62a9c2b))
* **storage:** make SQLite workspace registry authoritative ([#73](https://github.com/kuchmenko/workspace/issues/73)) ([a65c015](https://github.com/kuchmenko/workspace/commit/a65c015f919705cf3170d2cc6a784cfcc61276b6))
* **sync:** add signed workspace revision core ([2d3d18b](https://github.com/kuchmenko/workspace/commit/2d3d18ba40289ba840ae3c51726f06c8ba0cae3b))
* **sync:** include peer workspace exchange ([73c5fe5](https://github.com/kuchmenko/workspace/commit/73c5fe5efafc49a4c13679079d57205a73dcad58))
* **workspace:** support local root rebinding ([36f747b](https://github.com/kuchmenko/workspace/commit/36f747b8e9cdfcdc8e28be7ce6c232f8fedd49bf))


### Bug Fixes

* **alias:** refresh state after peer sync ([0235225](https://github.com/kuchmenko/workspace/commit/02352257146902b1b1e0f971b1d72f0417ef60aa))
* **alias:** refresh state after root rebind ([c3184cc](https://github.com/kuchmenko/workspace/commit/c3184cc08bf9a208734bbd2d8f198ea9529c048b))
* **alias:** refresh state after workspace sync ([ab17528](https://github.com/kuchmenko/workspace/commit/ab175289607b22ab9a360e38d839b0cfbebdf30f))
* **ci:** address lint and identity race ([d33ed9c](https://github.com/kuchmenko/workspace/commit/d33ed9c90b89173d6a0a160c418f84eae1752e35))
* **cli:** align network status columns ([ae852a1](https://github.com/kuchmenko/workspace/commit/ae852a17ea5260fbac80081879b6c803c1e5e8c9))
* **cli:** expose network administration directly ([2c117ca](https://github.com/kuchmenko/workspace/commit/2c117caedb38fb6a58b1f3d76947b9a7366a084c))
* **network:** align local and pairing transfer limits ([8dc5dc2](https://github.com/kuchmenko/workspace/commit/8dc5dc2434bbfe49e8887b19825cf0b4b21725bf))
* **network:** avoid local peer port collision ([e51ee9f](https://github.com/kuchmenko/workspace/commit/e51ee9f9dadf8fad2bc2e801b6c0de93ed63ebce))
* **network:** bound paginated workspace exchange ([058b831](https://github.com/kuchmenko/workspace/commit/058b8313535f2260d068e53c638a45512c4c72b6))
* **network:** confirm pairing on both devices concurrently ([d681791](https://github.com/kuchmenko/workspace/commit/d6817919d650f41c2b1c3fbda449ead3c8570ed0))
* **network:** harden discovered peer handling ([526d9a9](https://github.com/kuchmenko/workspace/commit/526d9a989b1215e3bd5cdea60e36af2582a7ea6e))
* **network:** keep membership history transportable ([deb9cbd](https://github.com/kuchmenko/workspace/commit/deb9cbd1e617ad9389aef2e60998515bcd1e4cd0))
* **network:** preserve invitation after invalid code ([5ecb2ca](https://github.com/kuchmenko/workspace/commit/5ecb2ca76f72bf34554abcded23a574c51df964a))
* **network:** preserve rejected sync status ([6e8869c](https://github.com/kuchmenko/workspace/commit/6e8869caf422068d80742a1fd20543830e11fa7e))
* **network:** protect pairing identity state ([b2771e5](https://github.com/kuchmenko/workspace/commit/b2771e59dd2086e46d0f02777a6a1350763d9822))
* **network:** recover pairing after response loss ([7e50a14](https://github.com/kuchmenko/workspace/commit/7e50a14585d075fd406908a2320208b3e7b9094c))
* **network:** reduce revision transfer batches ([bd9f97d](https://github.com/kuchmenko/workspace/commit/bd9f97d1b28de4217c0a6259379fe5d965d36bba))
* **network:** reject serving after local removal ([0535f39](https://github.com/kuchmenko/workspace/commit/0535f3973c3dbba72566d8f49bce1498d2d77896))
* **network:** scale peer frame deadlines ([1d4ecab](https://github.com/kuchmenko/workspace/commit/1d4ecab5e051a0db39716bff410d66ebcfe9fed8))
* **network:** scope IPv6 discovery endpoints ([dd3c300](https://github.com/kuchmenko/workspace/commit/dd3c30024efe6c06952a562fbd86de999f82093e))
* **network:** serialize pairing confirmation ([4731f58](https://github.com/kuchmenko/workspace/commit/4731f58411345120597467b76287d8e503369273))
* **network:** stage bounded workspace history exchange ([290887b](https://github.com/kuchmenko/workspace/commit/290887bf2cf2b527e375dabd0d26e27d1659d641))
* **network:** support IP device names in pairing ([7527260](https://github.com/kuchmenko/workspace/commit/7527260bd041000ebc740e84eaef58be68d366e4))
* **network:** tolerate unattached workspace peers ([f7ffc0c](https://github.com/kuchmenko/workspace/commit/f7ffc0c0ca04cf08c2744da52e8928a7bb4421e6))
* **network:** use stable peer service port ([1adb17b](https://github.com/kuchmenko/workspace/commit/1adb17bf6f32bffeca0b414451809c6e585582bb))
* **p2p:** harden synchronization invariants ([62e5c3d](https://github.com/kuchmenko/workspace/commit/62e5c3dc4de9d726ad3fdcbea30d0e0f07cc0b67))
* **p2p:** resolve remaining review findings ([a945caa](https://github.com/kuchmenko/workspace/commit/a945caaca11f548bc71fb2e8cf3300d2af928bd5))
* **p2p:** resolve synchronization review findings ([3317dd8](https://github.com/kuchmenko/workspace/commit/3317dd83ce7194b3437afebccd3d3f76a808cfb1))
* **registry:** accept migrated null access history ([6b70ce4](https://github.com/kuchmenko/workspace/commit/6b70ce4dfe83b7eb7276902090f3ae4dbd7c6e35))
* **registry:** authorize legacy revision branches ([51a1f44](https://github.com/kuchmenko/workspace/commit/51a1f443ddc95bff553822176a8c46f41f1e8517))
* **registry:** bound serialized revision size ([041feec](https://github.com/kuchmenko/workspace/commit/041feeca5d90e848e49213b665ed5855ef70e18f))
* **registry:** fail closed on network state errors ([0ff0665](https://github.com/kuchmenko/workspace/commit/0ff0665428d89092548e6201a0dc4354f81a7bb1))
* **registry:** migrate earliest revision schema ([0785ced](https://github.com/kuchmenko/workspace/commit/0785ced24e9a4140c281086ab9c20f6791cb42af))
* **registry:** preserve concurrent recovery authority ([50e3e3f](https://github.com/kuchmenko/workspace/commit/50e3e3fbf17245a64031fa6e5d351645868c61f8))
* **registry:** reject new revisions from removed devices ([40bb3a7](https://github.com/kuchmenko/workspace/commit/40bb3a7739a321e5437e6ad3dd833b2ea146f494))
* **registry:** reject unsafe workspace names ([d73e9be](https://github.com/kuchmenko/workspace/commit/d73e9bef41f74cdbf6b3845ecd4d4e80c951128a))
* **registry:** require durable revision authority ([f33a03e](https://github.com/kuchmenko/workspace/commit/f33a03ed8203b7cb7afe368ea08f991ded3dc9c1))
* **registry:** scope observation merge paths ([e71cc55](https://github.com/kuchmenko/workspace/commit/e71cc551a555beea948673f932beb513a6d3bcd6))
* **registry:** validate every mixed-epoch head pair ([17a566c](https://github.com/kuchmenko/workspace/commit/17a566cfdca674755c3c60f13a2d110630c2dba1))
* **registry:** validate mixed-epoch heads independently ([7c6c6fd](https://github.com/kuchmenko/workspace/commit/7c6c6fd7dede1a863fffffcf745beafbdfc578e2))
* **security:** sanitize peer-controlled diagnostics ([9c34728](https://github.com/kuchmenko/workspace/commit/9c34728f616d2dabc212655dc6494fa048833111))
* **security:** sanitize replicated conflict values ([de9b1f5](https://github.com/kuchmenko/workspace/commit/de9b1f5af7736882543b381f751f6bb5e8cbcbad))
* **security:** sanitize shared workspace metadata ([72e5629](https://github.com/kuchmenko/workspace/commit/72e5629600b4acbac659a032239a606f739a3ea8))
* **sync:** block on divergent registry heads ([da038df](https://github.com/kuchmenko/workspace/commit/da038df9fbc8524518f506a8f2f6d5a87510823c))
* **sync:** escape project labels in review UI ([7fcceee](https://github.com/kuchmenko/workspace/commit/7fcceee1f39c54ed58aff3b4e13d46f251b58da2))
* **sync:** escape replicated project labels ([4b866b8](https://github.com/kuchmenko/workspace/commit/4b866b8199f93bd4ab6bed591e0cd455450d8003))
* **sync:** fail rejected workspace exchanges ([0a7bd29](https://github.com/kuchmenko/workspace/commit/0a7bd291b1aaf4e73bcbd85cd55ddd585da181d1))
* **sync:** ignore moved upstream tags ([9942337](https://github.com/kuchmenko/workspace/commit/9942337afe432af43386985871e812ddc4926d91))
* **sync:** materialize projects received from peers ([d8e78e8](https://github.com/kuchmenko/workspace/commit/d8e78e87d60c2528c8f6103df7b4e48a7d1ee717))
* **sync:** override configured tag fetching ([9bbae2a](https://github.com/kuchmenko/workspace/commit/9bbae2afad1154251b498b3613a05ead4638cf13))
* **sync:** preflight before headless registry exchange ([7605413](https://github.com/kuchmenko/workspace/commit/7605413770c69285331868349b36dfaa2f6837af))
* **sync:** preserve final cancellation exit code ([b855a66](https://github.com/kuchmenko/workspace/commit/b855a66dfb2a16e1b21e9d58608a0bc61bdd6af9))
* **sync:** preserve refreshed plan semantics ([12a8de7](https://github.com/kuchmenko/workspace/commit/12a8de7fdc2f9297188d6792427f98ce237d1be9))
* **sync:** preserve safe conflict resolutions ([3ccceaa](https://github.com/kuchmenko/workspace/commit/3ccceaaada35c764a666a2a8d9962675fdd6bf6d))
* **sync:** reconcile origins and materialize peer projects ([44fa23e](https://github.com/kuchmenko/workspace/commit/44fa23e5d8d53600f17e908282fb28cd554a9f8a))
* **sync:** reconcile project origin changes ([a11fed7](https://github.com/kuchmenko/workspace/commit/a11fed7b937a0be9c0493680794d20cfd7ec0688))
* **sync:** reject credentialed origin promotion ([621b028](https://github.com/kuchmenko/workspace/commit/621b028e1af52f9934ccb1a23e9a4d7a2af38bf5))
* **sync:** remove obsolete peer wrapper ([4813821](https://github.com/kuchmenko/workspace/commit/481382135bca3af7fc40d5491ccd4fc33850b28e))
* **sync:** resolve origin divergence explicitly ([07f3975](https://github.com/kuchmenko/workspace/commit/07f397536a2b7ead1febd6dda1193a0be13fc03b))
* **sync:** retain dormant origin baselines ([e28eb39](https://github.com/kuchmenko/workspace/commit/e28eb3974f25157d3e16adf849839749322358fd))
* **sync:** track machine-local origin baselines ([bafd09c](https://github.com/kuchmenko/workspace/commit/bafd09cb4346e7d82cf6acf32a509f25b6bbf0c6))
* **sync:** validate remotes and tolerate offline discovery ([31b945d](https://github.com/kuchmenko/workspace/commit/31b945dd3834eb74fa830506e501516e30d65825))

## [0.10.0](https://github.com/kuchmenko/workspace/compare/v0.9.0...v0.10.0) (2026-08-11)


### Features

* **explorer:** add recency views and worktree lifecycle ([#65](https://github.com/kuchmenko/workspace/issues/65)) ([db20c2f](https://github.com/kuchmenko/workspace/commit/db20c2fb3e235e736dbc2a5fb678bdd1d8de314d))
* **explorer:** redesign navigation and command palette ([#70](https://github.com/kuchmenko/workspace/issues/70)) ([168a232](https://github.com/kuchmenko/workspace/commit/168a2327459f9f87052ebac4c90b9421f72fd1fb))

## [0.9.0](https://github.com/kuchmenko/workspace/compare/v0.8.1...v0.9.0) (2026-08-04)


### ⚠ BREAKING CHANGES

* explorer session discovery, resume, and prompt launch support are removed.
* **sync:** replace daemon with explicit interactive sync ([#7](https://github.com/kuchmenko/workspace/issues/7))

### Features

* **sync:** per-project push-only mirror remotes ([#2](https://github.com/kuchmenko/workspace/issues/2)) ([cef8fc9](https://github.com/kuchmenko/workspace/commit/cef8fc9c6023a2b57515229c966c2840befd1c60))
* **sync:** replace daemon with explicit interactive sync ([#7](https://github.com/kuchmenko/workspace/issues/7)) ([cccc360](https://github.com/kuchmenko/workspace/commit/cccc3608daef880ee62c719ec183304faa485941))


### Bug Fixes

* **installer:** download releases from GitHub ([b508e58](https://github.com/kuchmenko/workspace/commit/b508e58bbdcc43b2688b44cbd792a9da1ef48b04))
* **installer:** support private GitHub releases ([48a582a](https://github.com/kuchmenko/workspace/commit/48a582a615ff114d310b04cda47b772deae477b3))
* **sync:** stale ahead counter, uncommitted .gitattributes, dead branch param ([#5](https://github.com/kuchmenko/workspace/issues/5)) ([98f37dc](https://github.com/kuchmenko/workspace/commit/98f37dc58f4ba2315bc68ca02aebf9401a16c22e))


### Code Refactoring

* simplify explorer and remove unused code ([#64](https://github.com/kuchmenko/workspace/issues/64)) ([9d74ce3](https://github.com/kuchmenko/workspace/commit/9d74ce3b162c13b8371dccdabab26b5831badc9e))

## [0.8.0](https://github.com/kuchmenko/workspace/compare/v0.7.0...v0.8.0) (2026-06-01)


### Features

* **explorer:** unified launch sheet for chips, projects, and groups ([a116f5e](https://github.com/kuchmenko/workspace/commit/a116f5ed6dc781be2c0f09e68aa71e931c2e99ab))


### Bug Fixes

* **doctor:** repair malformed workspace toml ([#53](https://github.com/kuchmenko/workspace/issues/53)) ([9136ada](https://github.com/kuchmenko/workspace/commit/9136ada7e6278e001973e650866f68a642a9155d))

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
