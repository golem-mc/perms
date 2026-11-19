package core

import (
	"fmt"
	"strings"

	"github.com/golem-mc/golem/plugin"
)

// SplitStorage routes User, Group, Track, UUID, and Log operations to distinct Storage backends.
type SplitStorage struct {
	backends map[string]Storage // deduplicated by normalized method name
	user     Storage
	group    Storage
	track    Storage
	uuid     Storage
	log      Storage
}

// NewSplitStorage constructs a SplitStorage from cfg.
func NewSplitStorage(lp *LuckPerms, cfg SplitStorageConfig) (*SplitStorage, error) {
	ss := &SplitStorage{backends: make(map[string]Storage)}
	resolve := func(method string) (Storage, error) {
		key := strings.ToLower(strings.TrimSpace(method))
		if key == "" {
			key = "yaml"
		}
		if existing, ok := ss.backends[key]; ok {
			return existing, nil
		}
		st, err := lp.createSingleStorage(key)
		if err != nil {
			return nil, err
		}
		ss.backends[key] = st
		return st, nil
	}

	var err error
	if ss.user, err = resolve(cfg.User); err != nil {
		return nil, err
	}
	if ss.group, err = resolve(cfg.Group); err != nil {
		return nil, err
	}
	if ss.track, err = resolve(cfg.Track); err != nil {
		return nil, err
	}
	if ss.uuid, err = resolve(cfg.UUID); err != nil {
		return nil, err
	}
	if ss.log, err = resolve(cfg.Log); err != nil {
		return nil, err
	}
	return ss, nil
}

func (s *SplitStorage) Name() string { return "Split Storage" }

func (s *SplitStorage) DataDir() string {
	for _, b := range s.backends {
		if d := b.DataDir(); d != "" {
			return d
		}
	}
	return ""
}

// SQLBackend returns the first underlying SQLStorage, if any (for SQL/Postgres messaging).
func (s *SplitStorage) SQLBackend() *SQLStorage {
	for _, b := range s.backends {
		if sq, ok := b.(*SQLStorage); ok {
			return sq
		}
	}
	return nil
}

func (s *SplitStorage) Init() error {
	for name, b := range s.backends {
		if err := b.Init(); err != nil {
			return fmt.Errorf("split storage init (%s): %w", name, err)
		}
	}
	return nil
}

func (s *SplitStorage) Shutdown() error {
	var firstErr error
	for _, b := range s.backends {
		if err := b.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *SplitStorage) LoadUser(id plugin.UUID, username string) (*User, error) {
	return s.user.LoadUser(id, username)
}
func (s *SplitStorage) SaveUser(u *User) error { return s.user.SaveUser(u) }
func (s *SplitStorage) GetUniqueUsers() ([]plugin.UUID, error) {
	return s.user.GetUniqueUsers()
}
func (s *SplitStorage) SearchUserNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	return s.user.SearchUserNodes(match)
}

func (s *SplitStorage) CreateAndLoadGroup(name string) (*Group, error) {
	return s.group.CreateAndLoadGroup(name)
}
func (s *SplitStorage) LoadGroup(name string) (*Group, bool, error) {
	return s.group.LoadGroup(name)
}
func (s *SplitStorage) LoadAllGroups() error       { return s.group.LoadAllGroups() }
func (s *SplitStorage) SaveGroup(g *Group) error   { return s.group.SaveGroup(g) }
func (s *SplitStorage) DeleteGroup(g *Group) error { return s.group.DeleteGroup(g) }
func (s *SplitStorage) SearchGroupNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	return s.group.SearchGroupNodes(match)
}

func (s *SplitStorage) CreateAndLoadTrack(name string) (*Track, error) {
	return s.track.CreateAndLoadTrack(name)
}
func (s *SplitStorage) LoadTrack(name string) (*Track, bool, error) {
	return s.track.LoadTrack(name)
}
func (s *SplitStorage) LoadAllTracks() error       { return s.track.LoadAllTracks() }
func (s *SplitStorage) SaveTrack(t *Track) error   { return s.track.SaveTrack(t) }
func (s *SplitStorage) DeleteTrack(t *Track) error { return s.track.DeleteTrack(t) }

func (s *SplitStorage) SavePlayerData(id plugin.UUID, username string) error {
	return s.uuid.SavePlayerData(id, username)
}
func (s *SplitStorage) DeletePlayerData(id plugin.UUID) error {
	return s.uuid.DeletePlayerData(id)
}
func (s *SplitStorage) GetPlayerUniqueID(username string) (plugin.UUID, bool) {
	return s.uuid.GetPlayerUniqueID(username)
}
func (s *SplitStorage) GetPlayerName(id plugin.UUID) (string, bool) {
	return s.uuid.GetPlayerName(id)
}

func (s *SplitStorage) LogAction(a Action) error         { return s.log.LogAction(a) }
func (s *SplitStorage) GetLogEntries() ([]Action, error) { return s.log.GetLogEntries() }

func (s *SplitStorage) ApplyBulkUpdate(bu *BulkUpdate) error {
	if s.user == s.group {
		return s.user.ApplyBulkUpdate(bu)
	}
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataUsers {
		uCopy := *bu
		uCopy.DataType = BulkDataUsers
		if err := s.user.ApplyBulkUpdate(&uCopy); err != nil {
			return err
		}
		bu.Stats.AffectedNodes += uCopy.Stats.AffectedNodes
		bu.Stats.AffectedUsers += uCopy.Stats.AffectedUsers
	}
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataGroups {
		gCopy := *bu
		gCopy.DataType = BulkDataGroups
		if err := s.group.ApplyBulkUpdate(&gCopy); err != nil {
			return err
		}
		bu.Stats.AffectedNodes += gCopy.Stats.AffectedNodes
		bu.Stats.AffectedGroups += gCopy.Stats.AffectedGroups
	}
	return nil
}
