package search

// SearchState says whether searches can run. Its value is the wire word.
type SearchState string

const (
	SearchAvailable   SearchState = "available"
	SearchUnavailable SearchState = "unavailable"
)

// IndexingState says how far the index is behind processing. Its value is
// the wire word.
type IndexingState string

const (
	// IndexingStopped means the search process isn't running.
	IndexingStopped IndexingState = "stopped"
	// IndexingBuilding means no index was complete yet, so searches use
	// the one being built.
	IndexingBuilding   IndexingState = "building"
	IndexingCatchingUp IndexingState = "catching_up"
	IndexingUpToDate   IndexingState = "up_to_date"
)

// State is the search module's state for the status screen and the list.
type State struct {
	Search   SearchState
	Indexing IndexingState
}

// State returns the current state.
func (m *Module) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Module) setState(s State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(s)
}

// setStateLocked changes the state and logs each change. m.mu must be held.
func (m *Module) setStateLocked(s State) {
	if s == m.state {
		return
	}
	m.state = s
	m.config.Logger.Info("search state", "state", string(s.Search), "indexing", string(s.Indexing))
}

// setIndexing sets the indexing state from the generation receiving batches
// and the highest processed_seq.
func (m *Module) setIndexing(target *generation, highest int64) {
	indexing := IndexingUpToDate
	switch {
	case target.role == roleBuilding:
		indexing = IndexingBuilding
	case highest > target.cursor:
		indexing = IndexingCatchingUp
	}
	m.setState(State{Search: SearchAvailable, Indexing: indexing})
}
