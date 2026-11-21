package search

// SearchState says whether searches can run. Its value is the wire word.
type SearchState string

const (
	SearchAvailable   SearchState = "available"
	SearchUnavailable SearchState = "unavailable"
	// SearchNeedsRebuild means the index may be damaged or incomplete.
	// Searches still answer when an index is open, with a notice.
	SearchNeedsRebuild SearchState = "needs_rebuild"
)

// IndexingState says how far the index is behind processing. Its value is
// the wire word.
type IndexingState string

const (
	// IndexingStopped means the search process isn't running, or the index
	// needs a rebuild and none runs.
	IndexingStopped IndexingState = "stopped"
	// IndexingPaused means free space fell below 2 GiB and hasn't reached
	// 3 GiB since.
	IndexingPaused     IndexingState = "paused"
	IndexingRebuilding IndexingState = "rebuilding"
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

// refreshLocked derives the state from the module's fields, taking the
// first row that matches in each of the data model's tables, and logs each
// change. m.mu must be held.
func (m *Module) refreshLocked() {
	running := m.session != nil
	s := State{Search: SearchAvailable}
	switch {
	case m.needsRebuild != nil:
		s.Search = SearchNeedsRebuild
	case !running:
		s.Search = SearchUnavailable
	}
	switch {
	case !running, m.needsRebuild != nil && m.rebuild == nil, m.progress == "":
		s.Indexing = IndexingStopped
	case m.paused:
		s.Indexing = IndexingPaused
	case m.rebuild != nil:
		s.Indexing = IndexingRebuilding
	default:
		s.Indexing = m.progress
	}
	if s == m.state {
		return
	}
	m.state = s
	m.config.Logger.Info("search state", "state", string(s.Search), "indexing", string(s.Indexing))
}

// setProgress keeps how far the generations are, as the indexing loop last
// saw it: building, catching_up, or up_to_date.
func (m *Module) setProgress(progress IndexingState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress = progress
	m.refreshLocked()
}

// progressOf is how far feeds are: building while a generation without a
// rebuild builds, else catching up while the active one is behind highest.
func progressOf(feeds []*feed, highest int64) IndexingState {
	progress := IndexingUpToDate
	for _, f := range feeds {
		switch {
		case f.gen.role == roleBuilding && f.gen.rebuildID == nil:
			return IndexingBuilding
		case f.gen.role == roleActive && highest > f.gen.cursor:
			progress = IndexingCatchingUp
		}
	}
	return progress
}
