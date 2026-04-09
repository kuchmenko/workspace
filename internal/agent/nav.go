package agent

// Direction is one of the cardinal hjkl directions, plus DirNone as a
// sentinel for "no direction" (e.g. root has no back direction).
type Direction int

const (
	DirNone Direction = iota
	DirNorth
	DirEast
	DirSouth
	DirWest
)

// Opposite returns the cardinal opposite of d. The "back" direction at a
// freshly entered node is the opposite of the direction we moved to get
// there: if you moved north, you came from south, so back points south.
func (d Direction) Opposite() Direction {
	switch d {
	case DirNorth:
		return DirSouth
	case DirSouth:
		return DirNorth
	case DirEast:
		return DirWest
	case DirWest:
		return DirEast
	}
	return DirNone
}

func (d Direction) String() string {
	switch d {
	case DirNorth:
		return "N"
	case DirEast:
		return "E"
	case DirSouth:
		return "S"
	case DirWest:
		return "W"
	}
	return "."
}
