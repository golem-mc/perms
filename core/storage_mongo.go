package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoStorage implements Storage over MongoDB matching LuckPerms's MongoStorage schema.
type MongoStorage struct {
	lp     *LuckPerms
	prefix string
	client *mongo.Client
	db     *mongo.Database
}

// NewMongoStorage creates a MongoStorage backend from config.yml settings.
func NewMongoStorage(lp *LuckPerms) *MongoStorage {
	return &MongoStorage{
		lp:     lp,
		prefix: lp.Config().Data.MongoCollectionPrefix,
	}
}

func (s *MongoStorage) Name() string    { return "MongoDB" }
func (s *MongoStorage) DataDir() string { return "" }

func (s *MongoStorage) col(name string) *mongo.Collection {
	return s.db.Collection(s.prefix + name)
}

func (s *MongoStorage) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func (s *MongoStorage) Init() error {
	cfg := s.lp.Config().Data
	uri := cfg.MongoConnectionURI
	dbName := cfg.Database
	if dbName == "" {
		dbName = "minecraft"
	}

	if uri == "" {
		addr := defaultHostPort(cfg.Address, "27017")
		if cfg.Username != "" {
			uri = fmt.Sprintf("mongodb://%s:%s@%s/%s", url.QueryEscape(cfg.Username), url.QueryEscape(cfg.Password), addr, dbName)
		} else {
			uri = fmt.Sprintf("mongodb://%s/%s", addr, dbName)
		}
	} else if u, err := url.Parse(uri); err == nil {
		if pathDB := strings.TrimPrefix(u.Path, "/"); pathDB != "" {
			dbName = pathDB
		}
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return err
	}
	s.client = client
	s.db = client.Database(dbName)

	// Ensure username index on users collection.
	_, _ = s.col("users").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "name", Value: 1}},
	})
	return nil
}

func (s *MongoStorage) Shutdown() error {
	if s.client == nil {
		return nil
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.client.Disconnect(ctx)
}

// --- Document representations ---

type mongoNodeDoc struct {
	Key      string         `bson:"key"`
	Value    bool           `bson:"value"`
	Expiry   int64          `bson:"expiry,omitempty"`
	Contexts map[string]any `bson:"context,omitempty"`
}

type mongoUserDoc struct {
	ID           string         `bson:"_id"`
	Name         string         `bson:"name"`
	PrimaryGroup string         `bson:"primaryGroup"`
	Permissions  []mongoNodeDoc `bson:"permissions"`
}

type mongoGroupDoc struct {
	ID          string         `bson:"_id"`
	Permissions []mongoNodeDoc `bson:"permissions"`
}

type mongoTrackDoc struct {
	ID     string   `bson:"_id"`
	Groups []string `bson:"groups"`
}

type mongoActionDoc struct {
	Timestamp  int64  `bson:"timestamp"`
	SourceUUID string `bson:"source.uniqueId"`
	SourceName string `bson:"source.name"`
	TargetType string `bson:"target.type"`
	TargetUUID string `bson:"target.uniqueId,omitempty"`
	TargetName string `bson:"target.name"`
	Desc       string `bson:"description"`
}

func nodesToMongo(nodes []Node) []mongoNodeDoc {
	out := make([]mongoNodeDoc, len(nodes))
	for i, n := range nodes {
		d := mongoNodeDoc{Key: n.Key, Value: n.Value}
		if n.HasExpiry() {
			d.Expiry = n.Expiry.Unix()
		}
		if !n.Contexts.IsEmpty() {
			d.Contexts = ContextSetToJSONMap(n.Contexts)
		}
		out[i] = d
	}
	return out
}

func nodesFromMongo(docs []mongoNodeDoc) []Node {
	out := make([]Node, 0, len(docs))
	for _, d := range docs {
		if d.Key == "" {
			continue
		}
		n := ParseNode(d.Key, d.Value)
		if d.Expiry > 0 {
			n = n.WithExpiry(time.Unix(d.Expiry, 0).UTC())
		}
		if len(d.Contexts) > 0 {
			n = n.WithContexts(ContextSetFromJSONMap(d.Contexts))
		}
		out = append(out, n)
	}
	return out
}

// --- Users ---

func (s *MongoStorage) LoadUser(id plugin.UUID, username string) (*User, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	u := s.lp.Users.GetOrMake(id, username)
	var doc mongoUserDoc
	err := s.col("users").FindOne(ctx, bson.D{{Key: "_id", Value: id.String()}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		u.Data(DataNormal).SetContent(nil)
		u.SetStoredPrimaryGroup(DefaultGroupName)
		u.GiveDefaultIfNeeded()
		u.InvalidateCache()
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if doc.Name != "" && doc.Name != "null" && u.Username() == "" {
		u.SetUsername(doc.Name)
	}
	u.SetStoredPrimaryGroup(doc.PrimaryGroup)
	u.Data(DataNormal).SetContent(nodesFromMongo(doc.Permissions))
	u.GiveDefaultIfNeeded()
	u.InvalidateCache()
	if u.AuditTemporaryNodes() {
		_ = s.SaveUser(u)
	}
	return u, nil
}

func (s *MongoStorage) SaveUser(u *User) error {
	ctx, cancel := s.ctx()
	defer cancel()
	name := strings.ToLower(u.Username())
	if name == "" {
		name = "null"
	}
	doc := mongoUserDoc{
		ID:           u.UUID().String(),
		Name:         name,
		PrimaryGroup: u.StoredPrimaryGroup(),
		Permissions:  nodesToMongo(u.NormalNodes()),
	}
	_, err := s.col("users").ReplaceOne(ctx, bson.D{{Key: "_id", Value: doc.ID}}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *MongoStorage) GetUniqueUsers() ([]plugin.UUID, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("users").Find(ctx, bson.D{}, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var ids []plugin.UUID
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if cur.Decode(&row) == nil {
			if id, err := uuid.Parse(row.ID); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids, cur.Err()
}

func (s *MongoStorage) SearchUserNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("users").Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []NodeSearchResult
	for cur.Next(ctx) {
		var doc mongoUserDoc
		if cur.Decode(&doc) != nil {
			continue
		}
		id, _ := uuid.Parse(doc.ID)
		name := doc.Name
		if name == "" || name == "null" {
			name = doc.ID
		}
		for _, n := range nodesFromMongo(doc.Permissions) {
			if match(n) {
				out = append(out, NodeSearchResult{HolderType: HolderUser, HolderName: name, HolderUUID: id, Node: n})
			}
		}
	}
	return out, cur.Err()
}

// --- Groups ---

func (s *MongoStorage) CreateAndLoadGroup(name string) (*Group, error) {
	key := strings.ToLower(name)
	if g, ok, err := s.LoadGroup(key); err != nil || ok {
		return g, err
	}
	g := s.lp.Groups.GetOrMake(key)
	return g, s.SaveGroup(g)
}

func (s *MongoStorage) LoadGroup(name string) (*Group, bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	key := strings.ToLower(name)
	var doc mongoGroupDoc
	err := s.col("groups").FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	g := s.lp.Groups.GetOrMake(key)
	g.Data(DataNormal).SetContent(nodesFromMongo(doc.Permissions))
	g.InvalidateCache()
	return g, true, nil
}

func (s *MongoStorage) LoadAllGroups() error {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("groups").Find(ctx, bson.D{})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	loaded := make(map[string]struct{})
	for cur.Next(ctx) {
		var doc mongoGroupDoc
		if cur.Decode(&doc) != nil {
			continue
		}
		key := strings.ToLower(doc.ID)
		g := s.lp.Groups.GetOrMake(key)
		g.Data(DataNormal).SetContent(nodesFromMongo(doc.Permissions))
		loaded[key] = struct{}{}
	}
	for _, g := range s.lp.Groups.All() {
		if _, ok := loaded[g.Name()]; !ok && g.Name() != DefaultGroupName {
			s.lp.Groups.Unload(g.Name())
		}
	}
	if _, ok := loaded[DefaultGroupName]; !ok {
		if _, err := s.CreateAndLoadGroup(DefaultGroupName); err != nil {
			return err
		}
	}
	s.lp.InvalidateAllCaches()
	return cur.Err()
}

func (s *MongoStorage) SaveGroup(g *Group) error {
	ctx, cancel := s.ctx()
	defer cancel()
	doc := mongoGroupDoc{ID: g.Name(), Permissions: nodesToMongo(g.NormalNodes())}
	_, err := s.col("groups").ReplaceOne(ctx, bson.D{{Key: "_id", Value: g.Name()}}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *MongoStorage) DeleteGroup(g *Group) error {
	ctx, cancel := s.ctx()
	defer cancel()
	if _, err := s.col("groups").DeleteOne(ctx, bson.D{{Key: "_id", Value: g.Name()}}); err != nil {
		return err
	}
	s.lp.Groups.Unload(g.Name())
	s.lp.InvalidateAllCaches()
	return nil
}

func (s *MongoStorage) SearchGroupNodes(match func(Node) bool) ([]NodeSearchResult, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("groups").Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []NodeSearchResult
	for cur.Next(ctx) {
		var doc mongoGroupDoc
		if cur.Decode(&doc) != nil {
			continue
		}
		for _, n := range nodesFromMongo(doc.Permissions) {
			if match(n) {
				out = append(out, NodeSearchResult{HolderType: HolderGroup, HolderName: strings.ToLower(doc.ID), Node: n})
			}
		}
	}
	return out, cur.Err()
}

// --- Tracks ---

func (s *MongoStorage) CreateAndLoadTrack(name string) (*Track, error) {
	key := strings.ToLower(name)
	if t, ok, err := s.LoadTrack(key); err != nil || ok {
		return t, err
	}
	t := s.lp.Tracks.GetOrMake(key)
	return t, s.SaveTrack(t)
}

func (s *MongoStorage) LoadTrack(name string) (*Track, bool, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	key := strings.ToLower(name)
	var doc mongoTrackDoc
	err := s.col("tracks").FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	t := s.lp.Tracks.GetOrMake(key)
	t.SetGroups(doc.Groups)
	return t, true, nil
}

func (s *MongoStorage) LoadAllTracks() error {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("tracks").Find(ctx, bson.D{})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	loaded := make(map[string]struct{})
	for cur.Next(ctx) {
		var doc mongoTrackDoc
		if cur.Decode(&doc) != nil {
			continue
		}
		key := strings.ToLower(doc.ID)
		t := s.lp.Tracks.GetOrMake(key)
		t.SetGroups(doc.Groups)
		loaded[key] = struct{}{}
	}
	for _, t := range s.lp.Tracks.All() {
		if _, ok := loaded[t.Name()]; !ok {
			s.lp.Tracks.Unload(t.Name())
		}
	}
	return cur.Err()
}

func (s *MongoStorage) SaveTrack(t *Track) error {
	ctx, cancel := s.ctx()
	defer cancel()
	groups := t.Groups()
	if groups == nil {
		groups = []string{}
	}
	doc := mongoTrackDoc{ID: t.Name(), Groups: groups}
	_, err := s.col("tracks").ReplaceOne(ctx, bson.D{{Key: "_id", Value: t.Name()}}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *MongoStorage) DeleteTrack(t *Track) error {
	ctx, cancel := s.ctx()
	defer cancel()
	if _, err := s.col("tracks").DeleteOne(ctx, bson.D{{Key: "_id", Value: t.Name()}}); err != nil {
		return err
	}
	s.lp.Tracks.Unload(t.Name())
	return nil
}

// --- Player Data ---

func (s *MongoStorage) SavePlayerData(id plugin.UUID, username string) error {
	if username == "" {
		return nil
	}
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.col("users").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id.String()}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "name", Value: strings.ToLower(username)}}},
			{Key: "$setOnInsert", Value: bson.D{{Key: "primaryGroup", Value: DefaultGroupName}, {Key: "permissions", Value: []mongoNodeDoc{}}}},
		},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (s *MongoStorage) DeletePlayerData(id plugin.UUID) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.col("users").DeleteOne(ctx, bson.D{{Key: "_id", Value: id.String()}})
	return err
}

func (s *MongoStorage) GetPlayerUniqueID(username string) (plugin.UUID, bool) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc mongoUserDoc
	err := s.col("users").FindOne(ctx, bson.D{{Key: "name", Value: strings.ToLower(username)}}).Decode(&doc)
	if err != nil {
		return plugin.UUID{}, false
	}
	id, err := uuid.Parse(doc.ID)
	return id, err == nil
}

func (s *MongoStorage) GetPlayerName(id plugin.UUID) (string, bool) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc mongoUserDoc
	err := s.col("users").FindOne(ctx, bson.D{{Key: "_id", Value: id.String()}}).Decode(&doc)
	if err != nil || doc.Name == "" || doc.Name == "null" {
		return "", false
	}
	return doc.Name, true
}

// --- Action Log ---

func (s *MongoStorage) LogAction(a Action) error {
	ctx, cancel := s.ctx()
	defer cancel()
	doc := mongoActionDoc{
		Timestamp:  a.Timestamp.Unix(),
		SourceUUID: a.SourceUUID.String(),
		SourceName: a.SourceName,
		TargetType: string(a.TargetType),
		TargetName: a.TargetName,
		Desc:       a.Description,
	}
	if a.TargetUUID != nil {
		doc.TargetUUID = a.TargetUUID.String()
	}
	_, err := s.col("action").InsertOne(ctx, doc)
	return err
}

func (s *MongoStorage) GetLogEntries() ([]Action, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.col("action").Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []Action
	for cur.Next(ctx) {
		var doc mongoActionDoc
		if cur.Decode(&doc) != nil {
			continue
		}
		srcID, _ := uuid.Parse(doc.SourceUUID)
		var tgtID *plugin.UUID
		if doc.TargetUUID != "" {
			if parsed, err := uuid.Parse(doc.TargetUUID); err == nil {
				tgtID = &parsed
			}
		}
		out = append(out, Action{
			Timestamp:   time.Unix(doc.Timestamp, 0).UTC(),
			SourceUUID:  srcID,
			SourceName:  doc.SourceName,
			TargetType:  ActionTargetType(doc.TargetType),
			TargetUUID:  tgtID,
			TargetName:  doc.TargetName,
			Description: doc.Desc,
		})
	}
	return out, cur.Err()
}

// --- BulkUpdate ---

func (s *MongoStorage) ApplyBulkUpdate(bu *BulkUpdate) error {
	ctx, cancel := s.ctx()
	defer cancel()
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataGroups {
		cur, err := s.col("groups").Find(ctx, bson.D{})
		if err != nil {
			return err
		}
		for cur.Next(ctx) {
			var doc mongoGroupDoc
			if cur.Decode(&doc) != nil {
				continue
			}
			updated, changed := bu.Apply(nodesFromMongo(doc.Permissions), HolderGroup)
			if changed {
				doc.Permissions = nodesToMongo(updated)
				_, _ = s.col("groups").ReplaceOne(ctx, bson.D{{Key: "_id", Value: doc.ID}}, doc)
			}
		}
		cur.Close(ctx)
	}
	if bu.DataType == BulkDataAll || bu.DataType == BulkDataUsers {
		cur, err := s.col("users").Find(ctx, bson.D{})
		if err != nil {
			return err
		}
		for cur.Next(ctx) {
			var doc mongoUserDoc
			if cur.Decode(&doc) != nil {
				continue
			}
			updated, changed := bu.Apply(nodesFromMongo(doc.Permissions), HolderUser)
			if changed {
				doc.Permissions = nodesToMongo(updated)
				_, _ = s.col("users").ReplaceOne(ctx, bson.D{{Key: "_id", Value: doc.ID}}, doc)
			}
		}
		cur.Close(ctx)
	}
	return nil
}
