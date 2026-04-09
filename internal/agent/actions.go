package agent

// ActionsFor returns the quick-action map for a node, indexed by the
// hjkl direction that fires it. The Exec function is nil for actions
// not yet wired up — the launcher (layer 7) will replace these.
//
// Direction → action mapping is fixed by kind to support muscle memory.
func ActionsFor(n *Node) map[Direction]Action {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case KindProject:
		return map[Direction]Action{
			DirNorth: {Label: "[k] new"},
			DirEast:  {Label: "[l] worktree"},
			DirSouth: {Label: "[j] resume"},
			DirWest:  {Label: "[h] search"},
		}
	case KindWorktree:
		return map[Direction]Action{
			DirNorth: {Label: "[k] open"},
			DirEast:  {Label: "[l] delete"},
			DirSouth: {Label: "[j] resume"},
		}
	}
	return nil
}

// HasActions reports whether action mode should activate for this node.
func HasActions(n *Node) bool {
	return len(ActionsFor(n)) > 0
}
