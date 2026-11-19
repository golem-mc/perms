package core

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ExportSnapshot matches LuckPerms's Exporter.java JSON schema.
type ExportSnapshot struct {
	Metadata ExportMetadata         `json:"metadata"`
	Groups   map[string]ExportGroup `json:"groups,omitempty"`
	Tracks   map[string]ExportTrack `json:"tracks,omitempty"`
	Users    map[string]ExportUser  `json:"users,omitempty"`
}

type ExportMetadata struct {
	GeneratedBy string `json:"generatedBy"`
	GeneratedAt string `json:"generatedAt"`
}

type ExportGroup struct {
	Nodes []NodeJSON `json:"nodes"`
}

type ExportTrack struct {
	Groups []string `json:"groups"`
}

type ExportUser struct {
	Username     string     `json:"username,omitempty"`
	PrimaryGroup string     `json:"primaryGroup,omitempty"`
	Nodes        []NodeJSON `json:"nodes"`
}

// ExportSnapshotData builds an ExportSnapshot in memory.
func (lp *LuckPerms) ExportSnapshotData(generatedBy string, includeUsers, includeGroups bool) (ExportSnapshot, error) {
	snap := ExportSnapshot{
		Metadata: ExportMetadata{
			GeneratedBy: generatedBy,
			GeneratedAt: time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		},
	}

	if includeGroups {
		snap.Groups = make(map[string]ExportGroup)
		for _, g := range lp.Groups.All() {
			nodes := g.NormalNodes()
			jn := make([]NodeJSON, len(nodes))
			for i, n := range nodes {
				jn[i] = n.ToJSON()
			}
			snap.Groups[g.Name()] = ExportGroup{Nodes: jn}
		}
		snap.Tracks = make(map[string]ExportTrack)
		for _, t := range lp.Tracks.All() {
			groups := t.Groups()
			if groups == nil {
				groups = []string{}
			}
			snap.Tracks[t.Name()] = ExportTrack{Groups: groups}
		}
	}

	if includeUsers {
		snap.Users = make(map[string]ExportUser)
		ids, err := lp.Storage.GetUniqueUsers()
		if err != nil {
			return ExportSnapshot{}, err
		}
		for _, id := range ids {
			wasLoaded := lp.Users.GetIfLoaded(id) != nil
			u, err := lp.Storage.LoadUser(id, "")
			if err != nil {
				continue
			}
			nodes := u.NormalNodes()
			jn := make([]NodeJSON, len(nodes))
			for i, n := range nodes {
				jn[i] = n.ToJSON()
			}
			eu := ExportUser{
				Username: u.Username(),
				Nodes:    jn,
			}
			if pg := u.StoredPrimaryGroup(); !strings.EqualFold(pg, DefaultGroupName) {
				eu.PrimaryGroup = pg
			}
			snap.Users[id.String()] = eu
			if !wasLoaded && !lp.isOnline(id) {
				lp.Users.Unload(id)
			}
		}
	}
	return snap, nil
}

// ExportToBytebin uploads a gzipped ExportSnapshot to Bytebin and returns its paste key.
func (lp *LuckPerms) ExportToBytebin(generatedBy string, includeUsers, includeGroups bool) (string, error) {
	snap, err := lp.ExportSnapshotData(generatedBy, includeUsers, includeGroups)
	if err != nil {
		return "", err
	}
	return lp.Bytebin.PostJSON(snap, "export")
}

// ExportToFile writes a LuckPerms-compatible .json.gz export file into DataDir.
func (lp *LuckPerms) ExportToFile(fileName, generatedBy string, includeUsers, includeGroups bool) (string, error) {
	if !strings.HasSuffix(fileName, ".json.gz") && !strings.HasSuffix(fileName, ".json") {
		fileName += ".json.gz"
	}
	path := filepath.Join(lp.DataDir(), filepath.Base(fileName))

	snap, err := lp.ExportSnapshotData(generatedBy, includeUsers, includeGroups)
	if err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var w io.Writer = f
	var gz *gzip.Writer
	if strings.HasSuffix(path, ".gz") {
		gz = gzip.NewWriter(f)
		w = gz
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		if gz != nil {
			_ = gz.Close()
		}
		return "", err
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return "", err
		}
	}
	return path, nil
}

// ImportSummary reports counts imported by ImportFromFile.
type ImportSummary struct {
	Groups int
	Tracks int
	Users  int
}

// ImportFromBytebin downloads an exported snapshot from Bytebin and imports it.
func (lp *LuckPerms) ImportFromBytebin(code string, replace bool) (ImportSummary, error) {
	var snap ExportSnapshot
	if err := lp.Bytebin.GetJSON(code, &snap); err != nil {
		return ImportSummary{}, err
	}
	return lp.importSnapshot(snap, replace)
}

// ImportFromFile loads a LuckPerms .json.gz or .json export file from DataDir.
// If replace is true, existing nodes on imported groups/users are replaced instead of merged.
func (lp *LuckPerms) ImportFromFile(fileName string, replace bool) (ImportSummary, error) {
	candidates := []string{
		filepath.Join(lp.DataDir(), filepath.Base(fileName)),
		filepath.Join(lp.DataDir(), filepath.Base(fileName)+".json.gz"),
		filepath.Join(lp.DataDir(), filepath.Base(fileName)+".json"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		return ImportSummary{}, fmt.Errorf("file not found: %s", fileName)
	}

	f, err := os.Open(path)
	if err != nil {
		return ImportSummary{}, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return ImportSummary{}, err
		}
		defer gz.Close()
		r = gz
	}

	var snap ExportSnapshot
	if err := json.NewDecoder(r).Decode(&snap); err != nil {
		return ImportSummary{}, err
	}
	return lp.importSnapshot(snap, replace)
}

func (lp *LuckPerms) importSnapshot(snap ExportSnapshot, replace bool) (ImportSummary, error) {

	var summary ImportSummary
	for name, eg := range snap.Groups {
		g, err := lp.Storage.CreateAndLoadGroup(strings.ToLower(name))
		if err != nil {
			return summary, err
		}
		var nodes []Node
		for _, jn := range eg.Nodes {
			if n, ok := NodeFromJSON(jn); ok {
				nodes = append(nodes, n)
			}
		}
		if replace {
			g.Data(DataNormal).SetContent(nodes)
		} else {
			for _, n := range nodes {
				g.Data(DataNormal).Add(n)
			}
		}
		g.InvalidateCache()
		if err := lp.Storage.SaveGroup(g); err != nil {
			return summary, err
		}
		summary.Groups++
	}

	for name, et := range snap.Tracks {
		t, err := lp.Storage.CreateAndLoadTrack(strings.ToLower(name))
		if err != nil {
			return summary, err
		}
		if replace {
			t.SetGroups(et.Groups)
		} else {
			for _, g := range et.Groups {
				t.AppendGroup(g)
			}
		}
		if err := lp.Storage.SaveTrack(t); err != nil {
			return summary, err
		}
		summary.Tracks++
	}

	for idStr, eu := range snap.Users {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		wasLoaded := lp.Users.GetIfLoaded(id) != nil
		u, err := lp.Storage.LoadUser(id, eu.Username)
		if err != nil {
			return summary, err
		}
		if eu.Username != "" {
			u.SetUsername(eu.Username)
			_ = lp.Storage.SavePlayerData(id, eu.Username)
		}
		if eu.PrimaryGroup != "" {
			u.SetStoredPrimaryGroup(eu.PrimaryGroup)
		} else if replace {
			u.SetStoredPrimaryGroup(DefaultGroupName)
		}
		var nodes []Node
		for _, jn := range eu.Nodes {
			if n, ok := NodeFromJSON(jn); ok {
				nodes = append(nodes, n)
			}
		}
		if replace {
			u.Data(DataNormal).SetContent(nodes)
		} else {
			for _, n := range nodes {
				u.Data(DataNormal).Add(n)
			}
		}
		u.GiveDefaultIfNeeded()
		u.InvalidateCache()
		if err := lp.Storage.SaveUser(u); err != nil {
			return summary, err
		}
		if !wasLoaded && !lp.isOnline(id) {
			lp.Users.Unload(id)
		}
		summary.Users++
	}

	lp.InvalidateAllCaches()
	return summary, nil
}
