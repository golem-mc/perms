package core

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/nats-io/nats.go"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

// MessagingChannel is the standard LuckPerms channel/topic/routing key.
const MessagingChannel = "luckperms:update"

// Messenger is a transport implementation for LuckPerms cross-server messaging.
type Messenger interface {
	SendOutgoingMessage(encoded string) error
	Close() error
}

// MessengerProvider constructs a Messenger wired to an IncomingMessageConsumer.
type MessengerProvider interface {
	Name() string
	Obtain(consumer IncomingMessageConsumer) (Messenger, error)
}

// IncomingMessageConsumer receives encoded JSON messages from a Messenger.
type IncomingMessageConsumer interface {
	ConsumeIncomingMessageAsString(encoded string) bool
}

// wireMessage matches LuckPerms's LuckPermsMessagingService JSON envelope.
type wireMessage struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`
}

// MessagingService coordinates cross-server update, user-update, log, and custom messages.
type MessagingService struct {
	lp        *LuckPerms
	name      string
	messenger Messenger

	mu       sync.Mutex
	received map[plugin.UUID]time.Time

	customMu       sync.RWMutex
	customHandlers []func(channelID, payload string)
}

// NewMessagingService creates a MessagingService backed by provider.
func NewMessagingService(lp *LuckPerms, provider MessengerProvider) (*MessagingService, error) {
	ms := &MessagingService{
		lp:       lp,
		name:     provider.Name(),
		received: make(map[plugin.UUID]time.Time),
	}
	m, err := provider.Obtain(ms)
	if err != nil {
		return nil, err
	}
	ms.messenger = m
	return ms, nil
}

// Name returns the active messenger transport name.
func (ms *MessagingService) Name() string { return ms.name }

// Close shuts down the underlying messenger.
func (ms *MessagingService) Close() {
	if ms.messenger != nil {
		_ = ms.messenger.Close()
	}
}

// OnCustomMessage registers a handler for incoming custom messaging payloads.
func (ms *MessagingService) OnCustomMessage(fn func(channelID, payload string)) {
	ms.customMu.Lock()
	ms.customHandlers = append(ms.customHandlers, fn)
	ms.customMu.Unlock()
}

func (ms *MessagingService) markSeen(id plugin.UUID) bool {
	now := time.Now()
	ms.mu.Lock()
	defer ms.mu.Unlock()
	for k, exp := range ms.received {
		if now.After(exp) {
			delete(ms.received, k)
		}
	}
	if _, exists := ms.received[id]; exists {
		return false
	}
	ms.received[id] = now.Add(5 * time.Minute)
	return true
}

func (ms *MessagingService) send(msgType string, content any) error {
	id := uuid.New()
	ms.markSeen(id)
	env := wireMessage{ID: id.String(), Type: msgType}
	if content != nil {
		b, err := json.Marshal(content)
		if err != nil {
			return err
		}
		env.Content = b
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return ms.messenger.SendOutgoingMessage(string(b))
}

// PushUpdate broadcasts a full sync ping to other servers.
func (ms *MessagingService) PushUpdate() error {
	return ms.send("update", nil)
}

// PushUserUpdate broadcasts a specific user update ping.
func (ms *MessagingService) PushUserUpdate(u *User) error {
	return ms.send("userupdate", map[string]string{"userUuid": u.UUID().String()})
}

// PushLog broadcasts an Action log entry to other servers.
func (ms *MessagingService) PushLog(a Action) error {
	if !ms.lp.Config().PushLogEntries {
		return nil
	}
	payload := map[string]any{
		"timestamp": a.Timestamp.Unix(),
		"source": map[string]string{
			"uniqueId": a.SourceUUID.String(),
			"name":     a.SourceName,
		},
		"target": map[string]any{
			"type": string(a.TargetType),
			"name": a.TargetName,
		},
		"description": a.Description,
	}
	if a.TargetUUID != nil {
		payload["target"].(map[string]any)["uniqueId"] = a.TargetUUID.String()
	}
	return ms.send("log", payload)
}

// PushCustomPayload sends a custom channel payload across the network.
func (ms *MessagingService) PushCustomPayload(channelID, payload string) error {
	return ms.send("custom", map[string]string{
		"channelId": channelID,
		"payload":   payload,
	})
}

// ConsumeIncomingMessageAsString decodes and processes an incoming LuckPerms wire message.
func (ms *MessagingService) ConsumeIncomingMessageAsString(encoded string) bool {
	var env wireMessage
	if err := json.Unmarshal([]byte(encoded), &env); err != nil {
		ms.lp.Log.Warn("Unable to decode incoming messaging service message", "err", err)
		return false
	}
	id, err := uuid.Parse(env.ID)
	if err != nil {
		return false
	}
	if !ms.markSeen(id) {
		return false
	}

	switch env.Type {
	case "update":
		ms.lp.Log.Info("[Messaging] Received update ping", "id", id)
		_ = ms.lp.Sync()
		return true

	case "userupdate":
		var body struct {
			UserUUID string `json:"userUuid"`
		}
		if json.Unmarshal(env.Content, &body) != nil {
			return false
		}
		uid, err := uuid.Parse(body.UserUUID)
		if err != nil {
			return false
		}
		if u := ms.lp.Users.GetIfLoaded(uid); u != nil {
			ms.lp.Log.Info("[Messaging] Received user update ping", "user", u.FriendlyName(), "id", id)
			_, _ = ms.lp.Storage.LoadUser(uid, u.Username())
			ms.lp.refreshAllAutoOp()
		}
		return true

	case "log":
		var body struct {
			Timestamp int64 `json:"timestamp"`
			Source    struct {
				UniqueID string `json:"uniqueId"`
				Name     string `json:"name"`
			} `json:"source"`
			Target struct {
				Type     string `json:"type"`
				UniqueID string `json:"uniqueId"`
				Name     string `json:"name"`
			} `json:"target"`
			Description string `json:"description"`
		}
		if json.Unmarshal(env.Content, &body) != nil {
			return false
		}
		srcID, _ := uuid.Parse(body.Source.UniqueID)
		var tgtID *plugin.UUID
		if body.Target.UniqueID != "" {
			if p, err := uuid.Parse(body.Target.UniqueID); err == nil {
				tgtID = &p
			}
		}
		act := Action{
			Timestamp:   time.Unix(body.Timestamp, 0).UTC(),
			SourceUUID:  srcID,
			SourceName:  body.Source.Name,
			TargetType:  ActionTargetType(body.Target.Type),
			TargetUUID:  tgtID,
			TargetName:  body.Target.Name,
			Description: body.Description,
		}
		if ms.lp.Config().BroadcastReceivedLogEntries {
			ms.lp.Actions.BroadcastRemote(act)
		}
		return true

	case "custom":
		var body struct {
			ChannelID string `json:"channelId"`
			Payload   string `json:"payload"`
		}
		if json.Unmarshal(env.Content, &body) != nil {
			return false
		}
		ms.customMu.RLock()
		handlers := append([]func(string, string){}, ms.customHandlers...)
		ms.customMu.RUnlock()
		for _, fn := range handlers {
			fn(body.ChannelID, body.Payload)
		}
		return true

	default:
		return false
	}
}

// --- Java DataOutput.writeUTF / DataInput.readUTF framing (2-byte big-endian length prefix) ---

func encodeJavaUTF(s string) []byte {
	b := []byte(s)
	out := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(out[:2], uint16(len(b)))
	copy(out[2:], b)
	return out
}

func decodeJavaUTF(data []byte) (string, error) {
	if len(data) < 2 {
		return "", errors.New("short UTF frame")
	}
	n := int(binary.BigEndian.Uint16(data[:2]))
	if len(data) < 2+n {
		return "", errors.New("truncated UTF frame")
	}
	return string(data[2 : 2+n]), nil
}

// --- 1. Plugin Message Messenger (via golem Server.Messenger) ---

type pluginMsgProvider struct{ lp *LuckPerms }

func (p *pluginMsgProvider) Name() string { return "PluginMessage" }
func (p *pluginMsgProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	if p.lp.Server == nil {
		return nil, errors.New("pluginmsg requires a running golem server")
	}
	ch := plugin.Key{Namespace: "luckperms", Path: "update"}
	m := &pluginMsgMessenger{lp: p.lp, channel: ch, consumer: consumer}
	p.lp.Server.Messenger().Listen(ch, func(_ plugin.Player, data []byte) {
		if str, err := decodeJavaUTF(data); err == nil {
			consumer.ConsumeIncomingMessageAsString(str)
		}
	})
	return m, nil
}

type pluginMsgMessenger struct {
	lp       *LuckPerms
	channel  plugin.Key
	consumer IncomingMessageConsumer
}

func (m *pluginMsgMessenger) SendOutgoingMessage(encoded string) error {
	players := m.lp.Server.Players()
	if len(players) == 0 {
		return nil
	}
	frame := encodeJavaUTF(encoded)
	// Send over the first online player's connection (standard Bungee/Velocity plugin messaging).
	m.lp.Server.Messenger().Send(players[0], m.channel, frame)
	return nil
}

func (m *pluginMsgMessenger) Close() error { return nil }

// --- 2. SQL Queue Messenger (MySQL/MariaDB/SQLite) ---

type sqlMessengerProvider struct {
	lp  *LuckPerms
	sql *SQLStorage
}

func (p *sqlMessengerProvider) Name() string { return "SQL" }
func (p *sqlMessengerProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	m := &sqlMessenger{
		lp:       p.lp,
		sql:      p.sql,
		table:    p.sql.TablePrefix() + "messenger",
		consumer: consumer,
	}
	if err := m.init(); err != nil {
		return nil, err
	}
	return m, nil
}

type sqlMessenger struct {
	lp       *LuckPerms
	sql      *SQLStorage
	table    string
	consumer IncomingMessageConsumer

	mu     sync.Mutex
	lastID int64
	closed atomic.Bool
	poll   plugin.Task
	clean  plugin.Task
}

func (m *sqlMessenger) init() error {
	var createSQL string
	switch m.sql.Dialect() {
	case DialectSQLite:
		createSQL = fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (`id` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, `time` BIGINT NOT NULL, `msg` TEXT NOT NULL)", m.table)
	case DialectPostgreSQL:
		createSQL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" ("id" SERIAL PRIMARY KEY NOT NULL, "time" BIGINT NOT NULL, "msg" TEXT NOT NULL)`, m.table)
	default:
		createSQL = fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (`id` INT AUTO_INCREMENT NOT NULL, `time` BIGINT NOT NULL, `msg` TEXT NOT NULL, PRIMARY KEY (`id`)) DEFAULT CHARSET = utf8mb4", m.table)
	}
	if _, err := m.sql.DB().Exec(createSQL); err != nil {
		return err
	}
	var latest sql.NullInt64
	_ = m.sql.DB().QueryRow(m.sql.q(fmt.Sprintf("SELECT MAX(`id`) FROM `%s`", m.table))).Scan(&latest)
	if latest.Valid {
		m.lastID = latest.Int64
	}
	if m.lp.Server != nil {
		m.poll = m.lp.Server.Scheduler().RunAsyncTimer(1*time.Second, 1*time.Second, m.pollMessages)
		m.clean = m.lp.Server.Scheduler().RunAsyncTimer(30*time.Second, 30*time.Second, m.housekeeping)
	}
	return nil
}

func (m *sqlMessenger) SendOutgoingMessage(encoded string) error {
	if m.closed.Load() {
		return nil
	}
	_, err := m.sql.DB().Exec(m.sql.q(fmt.Sprintf("INSERT INTO `%s` (`time`, `msg`) VALUES(?, ?)", m.table)), time.Now().Unix(), encoded)
	return err
}

func (m *sqlMessenger) pollMessages() {
	if m.closed.Load() {
		return
	}
	m.mu.Lock()
	last := m.lastID
	m.mu.Unlock()

	cutoff := time.Now().Add(-30 * time.Second).Unix()
	rows, err := m.sql.DB().Query(m.sql.q(fmt.Sprintf("SELECT `id`, `msg` FROM `%s` WHERE `id` > ? AND `time` > ?", m.table)), last, cutoff)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var msg string
		if rows.Scan(&id, &msg) == nil {
			m.mu.Lock()
			if id > m.lastID {
				m.lastID = id
			}
			m.mu.Unlock()
			m.consumer.ConsumeIncomingMessageAsString(msg)
		}
	}
}

func (m *sqlMessenger) housekeeping() {
	if m.closed.Load() {
		return
	}
	cutoff := time.Now().Add(-60 * time.Second).Unix()
	_, _ = m.sql.DB().Exec(m.sql.q(fmt.Sprintf("DELETE FROM `%s` WHERE `time` < ?", m.table)), cutoff)
}

func (m *sqlMessenger) Close() error {
	m.closed.Store(true)
	if m.poll != nil {
		m.poll.Cancel()
	}
	if m.clean != nil {
		m.clean.Cancel()
	}
	return nil
}

// --- 3. PostgreSQL LISTEN/NOTIFY Messenger ---

type postgresMessengerProvider struct {
	lp  *LuckPerms
	sql *SQLStorage
}

func (p *postgresMessengerProvider) Name() string { return "PostgreSQL" }
func (p *postgresMessengerProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	l := pq.NewListener(p.sql.DSN(), 2*time.Second, time.Minute, nil)
	if err := l.Listen(MessagingChannel); err != nil {
		_ = l.Close()
		return nil, err
	}
	m := &postgresMessenger{lp: p.lp, sql: p.sql, listener: l, consumer: consumer, done: make(chan struct{})}
	go m.loop()
	return m, nil
}

type postgresMessenger struct {
	lp       *LuckPerms
	sql      *SQLStorage
	listener *pq.Listener
	consumer IncomingMessageConsumer
	done     chan struct{}
}

func (m *postgresMessenger) loop() {
	for {
		select {
		case <-m.done:
			return
		case n, ok := <-m.listener.Notify:
			if !ok {
				return
			}
			if n != nil && n.Channel == MessagingChannel {
				m.consumer.ConsumeIncomingMessageAsString(n.Extra)
			}
		}
	}
}

func (m *postgresMessenger) SendOutgoingMessage(encoded string) error {
	_, err := m.sql.DB().Exec(`SELECT pg_notify($1, $2)`, MessagingChannel, encoded)
	return err
}

func (m *postgresMessenger) Close() error {
	close(m.done)
	return m.listener.Close()
}

// --- 4. Redis Pub/Sub Messenger ---

type redisMessengerProvider struct {
	lp  *LuckPerms
	cfg RedisConfig
}

func (p *redisMessengerProvider) Name() string { return "Redis" }
func (p *redisMessengerProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	var tlsCfg *tls.Config
	if p.cfg.SSL {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	var client redis.UniversalClient
	switch {
	case p.cfg.SentinelEnabled:
		addrs := make([]string, len(p.cfg.SentinelAddrs))
		for i, a := range p.cfg.SentinelAddrs {
			addrs[i] = defaultHostPort(a, "26379")
		}
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       p.cfg.SentinelMaster,
			SentinelAddrs:    addrs,
			SentinelUsername: p.cfg.SentinelUsername,
			SentinelPassword: p.cfg.SentinelPassword,
			Username:         p.cfg.Username,
			Password:         p.cfg.Password,
			TLSConfig:        tlsCfg,
		})
	case len(p.cfg.Addresses) > 1:
		addrs := make([]string, len(p.cfg.Addresses))
		for i, a := range p.cfg.Addresses {
			addrs[i] = defaultHostPort(a, "6379")
		}
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:     addrs,
			Username:  p.cfg.Username,
			Password:  p.cfg.Password,
			TLSConfig: tlsCfg,
		})
	default:
		addr := p.cfg.Address
		if len(p.cfg.Addresses) == 1 {
			addr = p.cfg.Addresses[0]
		}
		client = redis.NewClient(&redis.Options{
			Addr:      defaultHostPort(addr, "6379"),
			Username:  p.cfg.Username,
			Password:  p.cfg.Password,
			TLSConfig: tlsCfg,
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	pubsub := client.Subscribe(context.Background(), MessagingChannel)
	m := &redisMessenger{client: client, pubsub: pubsub, consumer: consumer, done: make(chan struct{})}
	go m.loop()
	return m, nil
}

type redisMessenger struct {
	client   redis.UniversalClient
	pubsub   *redis.PubSub
	consumer IncomingMessageConsumer
	done     chan struct{}
}

func (m *redisMessenger) loop() {
	ch := m.pubsub.Channel()
	for {
		select {
		case <-m.done:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			m.consumer.ConsumeIncomingMessageAsString(msg.Payload)
		}
	}
}

func (m *redisMessenger) SendOutgoingMessage(encoded string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.client.Publish(ctx, MessagingChannel, encoded).Err()
}

func (m *redisMessenger) Close() error {
	close(m.done)
	_ = m.pubsub.Close()
	return m.client.Close()
}

// --- 5. NATS Pub/Sub Messenger ---

type natsMessengerProvider struct {
	lp  *LuckPerms
	cfg NatsConfig
}

func (p *natsMessengerProvider) Name() string { return "NATS" }
func (p *natsMessengerProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	opts := []nats.Option{nats.Name("LuckPerms"), nats.ReconnectWait(5 * time.Second), nats.MaxReconnects(-1)}
	if p.cfg.Username != "" && p.cfg.Password != "" {
		opts = append(opts, nats.UserInfo(p.cfg.Username, p.cfg.Password))
	}
	if p.cfg.Token != "" {
		opts = append(opts, nats.Token(p.cfg.Token))
	}
	if p.cfg.SSL {
		opts = append(opts, nats.Secure(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	conn, err := nats.Connect("nats://"+defaultHostPort(p.cfg.Address, "4222"), opts...)
	if err != nil {
		return nil, err
	}
	sub, err := conn.Subscribe(MessagingChannel, func(msg *nats.Msg) {
		if str, err := decodeJavaUTF(msg.Data); err == nil {
			consumer.ConsumeIncomingMessageAsString(str)
		}
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &natsMessenger{conn: conn, sub: sub}, nil
}

type natsMessenger struct {
	conn *nats.Conn
	sub  *nats.Subscription
}

func (m *natsMessenger) SendOutgoingMessage(encoded string) error {
	return m.conn.Publish(MessagingChannel, encodeJavaUTF(encoded))
}

func (m *natsMessenger) Close() error {
	_ = m.sub.Unsubscribe()
	m.conn.Close()
	return nil
}

// --- 6. RabbitMQ AMQP Messenger ---

type rabbitMQMessengerProvider struct {
	lp  *LuckPerms
	cfg RabbitMQConfig
}

func (p *rabbitMQMessengerProvider) Name() string { return "RabbitMQ" }
func (p *rabbitMQMessengerProvider) Obtain(consumer IncomingMessageConsumer) (Messenger, error) {
	vhost := p.cfg.VHost
	if vhost == "" {
		vhost = "/"
	}
	uri := fmt.Sprintf("amqp://%s:%s@%s/%s",
		url.QueryEscape(p.cfg.Username),
		url.QueryEscape(p.cfg.Password),
		defaultHostPort(p.cfg.Address, "5672"),
		url.PathEscape(strings.TrimPrefix(vhost, "/")),
	)
	conn, err := amqp.Dial(uri)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ch.ExchangeDeclare("luckperms", "topic", false, true, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err := ch.QueueBind(q.Name, MessagingChannel, "luckperms", false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	deliveries, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	m := &rabbitMQMessenger{conn: conn, ch: ch, done: make(chan struct{})}
	go func() {
		for {
			select {
			case <-m.done:
				return
			case d, ok := <-deliveries:
				if !ok {
					return
				}
				if str, err := decodeJavaUTF(d.Body); err == nil {
					consumer.ConsumeIncomingMessageAsString(str)
				}
			}
		}
	}()
	return m, nil
}

type rabbitMQMessenger struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	done chan struct{}
}

func (m *rabbitMQMessenger) SendOutgoingMessage(encoded string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.ch.PublishWithContext(ctx, "luckperms", MessagingChannel, false, false, amqp.Publishing{
		Body: encodeJavaUTF(encoded),
	})
}

func (m *rabbitMQMessenger) Close() error {
	close(m.done)
	_ = m.ch.Close()
	return m.conn.Close()
}

// --- Messaging initialization from config ---

func (lp *LuckPerms) initMessaging() {
	cfg := lp.Config()
	svc := strings.ToLower(strings.TrimSpace(cfg.MessagingService))
	if svc == "none" {
		return
	}

	var provider MessengerProvider
	switch svc {
	case "pluginmsg":
		provider = &pluginMsgProvider{lp: lp}
	case "redis":
		provider = &redisMessengerProvider{lp: lp, cfg: cfg.Redis}
	case "nats":
		provider = &natsMessengerProvider{lp: lp, cfg: cfg.Nats}
	case "rabbitmq":
		provider = &rabbitMQMessengerProvider{lp: lp, cfg: cfg.RabbitMQ}
	case "sql":
		if sq := lp.sqlStorage(); sq != nil {
			provider = &sqlMessengerProvider{lp: lp, sql: sq}
		}
	case "postgres", "postgresql":
		if sq := lp.sqlStorage(); sq != nil && sq.Dialect() == DialectPostgreSQL {
			provider = &postgresMessengerProvider{lp: lp, sql: sq}
		}
	case "auto", "notsql":
		switch {
		case cfg.Redis.Enabled:
			provider = &redisMessengerProvider{lp: lp, cfg: cfg.Redis}
		case cfg.Nats.Enabled:
			provider = &natsMessengerProvider{lp: lp, cfg: cfg.Nats}
		case cfg.RabbitMQ.Enabled:
			provider = &rabbitMQMessengerProvider{lp: lp, cfg: cfg.RabbitMQ}
		case svc == "auto":
			if sq := lp.sqlStorage(); sq != nil {
				switch sq.Dialect() {
				case DialectPostgreSQL:
					provider = &postgresMessengerProvider{lp: lp, sql: sq}
				case DialectMySQL, DialectMariaDB:
					provider = &sqlMessengerProvider{lp: lp, sql: sq}
				}
			}
		}
	}

	if provider == nil {
		return
	}
	ms, err := NewMessagingService(lp, provider)
	if err != nil {
		lp.Log.Warn("Failed to initialize messaging service", "service", provider.Name(), "err", err)
		return
	}
	lp.Messaging = ms
	lp.Log.Info("Messaging service enabled", "service", ms.Name())
}

func (lp *LuckPerms) sqlStorage() *SQLStorage {
	if sq, ok := lp.Storage.(*SQLStorage); ok {
		return sq
	}
	if sp, ok := lp.Storage.(*SplitStorage); ok {
		return sp.SQLBackend()
	}
	return nil
}

// PushUpdateAsync pushes an update ping over the active messaging service (if enabled and auto-push-updates is true).
func (lp *LuckPerms) PushUpdateAsync() {
	if lp.Messaging == nil || !lp.Config().AutoPushUpdates {
		return
	}
	go func() {
		if err := lp.Messaging.PushUpdate(); err != nil {
			lp.Log.Warn("Failed to push messaging update", "err", err)
		}
	}()
}

// PushUserUpdateAsync pushes a user update ping over the active messaging service.
func (lp *LuckPerms) PushUserUpdateAsync(u *User) {
	if lp.Messaging == nil || !lp.Config().AutoPushUpdates {
		return
	}
	go func() {
		if err := lp.Messaging.PushUserUpdate(u); err != nil {
			lp.Log.Warn("Failed to push user messaging update", "err", err)
		}
	}()
}

// keep database/sql import for sql.NullInt64
var _ = (*sql.NullInt64)(nil)
