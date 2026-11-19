package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

// TraversalAlgorithm selects how the inheritance graph is traversed.
type TraversalAlgorithm string

const (
	TraversalDepthFirstPreOrder  TraversalAlgorithm = "depth-first-pre-order"
	TraversalDepthFirstPostOrder TraversalAlgorithm = "depth-first-post-order"
	TraversalBreadthFirst        TraversalAlgorithm = "breadth-first"
)

// PrimaryGroupMode selects how a user's primary group is calculated.
type PrimaryGroupMode string

const (
	PrimaryGroupStored             PrimaryGroupMode = "stored"
	PrimaryGroupParentsByWeight    PrimaryGroupMode = "parents-by-weight"
	PrimaryGroupAllParentsByWeight PrimaryGroupMode = "all-parents-by-weight"
)

// DuplicateRemoval controls how duplicate prefixes/suffixes in a stack are removed.
type DuplicateRemoval string

const (
	DuplicateRetainAll DuplicateRemoval = "retain-all"
	DuplicateFirstOnly DuplicateRemoval = "first-only"
	DuplicateLastOnly  DuplicateRemoval = "last-only"
)

// MetaValueSelection controls how multiple meta values for the same key are resolved.
type MetaValueSelection string

const (
	MetaSelectInheritance   MetaValueSelection = "inheritance"
	MetaSelectHighestNumber MetaValueSelection = "highest-number"
	MetaSelectLowestNumber  MetaValueSelection = "lowest-number"
)

// MetaStackConfig defines prefix or suffix stacking formatting.
type MetaStackConfig struct {
	Format       []string
	Duplicates   DuplicateRemoval
	StartSpacer  string
	MiddleSpacer string
	EndSpacer    string
}

// DatabaseConfig holds remote SQL / MongoDB credentials and pool settings.
type DatabaseConfig struct {
	Address               string
	Database              string
	Username              string
	Password              string
	TablePrefix           string
	MongoCollectionPrefix string
	MongoConnectionURI    string
	MaxPoolSize           int
	MinIdle               int
	MaxLifetimeMillis     int
	ConnectionTimeoutMs   int
	Properties            map[string]string
}

// SplitStorageConfig defines per-data-type storage methods.
type SplitStorageConfig struct {
	Enabled bool
	User    string
	Group   string
	Track   string
	UUID    string
	Log     string
}

// RedisConfig holds Redis pub/sub connection settings.
type RedisConfig struct {
	Enabled          bool
	Address          string
	Addresses        []string
	Username         string
	Password         string
	SSL              bool
	SentinelEnabled  bool
	SentinelMaster   string
	SentinelAddrs    []string
	SentinelUsername string
	SentinelPassword string
}

// NatsConfig holds NATS pub/sub connection settings.
type NatsConfig struct {
	Enabled  bool
	Address  string
	Username string
	Password string
	Token    string
	SSL      bool
}

// RabbitMQConfig holds RabbitMQ AMQP connection settings.
type RabbitMQConfig struct {
	Enabled  bool
	Address  string
	VHost    string
	Username string
	Password string
}

// Config holds LuckPerms's configuration from config.yml and contexts.json.
type Config struct {
	Server             string
	UseServerUUIDCache bool
	StorageMethod      string
	Data               DatabaseConfig
	SplitStorage       SplitStorageConfig
	SyncMinutes        int
	WatchFiles         bool

	MessagingService            string
	AutoPushUpdates             bool
	PushLogEntries              bool
	BroadcastReceivedLogEntries bool
	Redis                       RedisConfig
	Nats                        NatsConfig
	RabbitMQ                    RabbitMQConfig

	TemporaryAddBehaviour TemporaryNodeMergeStrategy
	PrimaryGroupCalc      PrimaryGroupMode

	ArgumentBasedCommandPerms         bool
	RequireSenderGroupMembershipToMod bool

	LogNotify            bool
	LogNotifyFilterRegex []*regexp.Regexp

	AutoInstallTranslations bool

	PrefixFormatting MetaStackConfig
	SuffixFormatting MetaStackConfig

	InheritanceTraversal      TraversalAlgorithm
	PostTraversalSort         bool
	ContextSatisfyMode        ContextSatisfyMode
	DisabledContexts          map[string]struct{}
	IncludeGlobal             bool
	IncludeGlobalWorld        bool
	ApplyGlobalGroups         bool
	ApplyGlobalWorldGroups    bool
	MetaValueSelectionDefault MetaValueSelection
	MetaValueSelectionPerKey  map[string]MetaValueSelection
	ApplyWildcards            bool
	ApplySpongeWildcards      bool
	ApplyDefaultNegBeforeWild bool
	ApplyRegex                bool
	ApplyShorthand            bool
	ApplyChildPermissions     bool
	ApplyDefaultPermissions   bool
	WorldRewrites             map[string]string
	GroupWeights              map[string]int

	EnableOps       bool
	AutoOp          bool
	CommandsAllowOp bool

	DebugLogins                bool
	AllowInvalidUsernames      bool
	SkipBulkUpdateConfirmation bool
	DisableBulkUpdate          bool
	PreventPrimaryGroupRemoval bool
	ResolveCommandSelectors    bool
	CommandsRateLimit          bool

	ReadOnlyPlayers bool
	ReadOnlyConsole bool
	DisablePlayers  bool
	DisableConsole  bool

	// Web service URLs (customizable for self-hosting or testing).
	BytebinURL       string
	BytesocksURL     string
	WebEditorURL     string
	VerboseViewerURL string
	TreeViewerURL    string

	// Loaded from contexts.json (if present)
	StaticContexts  ContextSet
	DefaultContexts ContextSet
}

// DefaultConfig returns a Config populated with LuckPerms defaults.
func DefaultConfig() *Config {
	return &Config{
		Server:             GlobalContextValue,
		UseServerUUIDCache: true,
		StorageMethod:      "yaml",
		Data: DatabaseConfig{
			Address:             "localhost",
			Database:            "minecraft",
			Username:            "root",
			TablePrefix:         "luckperms_",
			MaxPoolSize:         10,
			MinIdle:             10,
			MaxLifetimeMillis:   1800000,
			ConnectionTimeoutMs: 5000,
			Properties:          map[string]string{},
		},
		SplitStorage: SplitStorageConfig{
			User:  "yaml",
			Group: "yaml",
			Track: "yaml",
			UUID:  "yaml",
			Log:   "yaml",
		},
		SyncMinutes:                 -1,
		WatchFiles:                  true,
		MessagingService:            "auto",
		AutoPushUpdates:             true,
		PushLogEntries:              true,
		BroadcastReceivedLogEntries: true,
		Redis: RedisConfig{
			Address:        "localhost",
			SentinelMaster: "mymaster",
		},
		Nats: NatsConfig{
			Address: "localhost",
		},
		RabbitMQ: RabbitMQConfig{
			Address:  "localhost",
			VHost:    "/",
			Username: "guest",
			Password: "guest",
		},
		TemporaryAddBehaviour:     TempMergeDeny,
		PrimaryGroupCalc:          PrimaryGroupParentsByWeight,
		LogNotify:                 true,
		AutoInstallTranslations:   false,
		PrefixFormatting:          MetaStackConfig{Format: []string{"highest"}, Duplicates: DuplicateFirstOnly, MiddleSpacer: " "},
		SuffixFormatting:          MetaStackConfig{Format: []string{"highest"}, Duplicates: DuplicateFirstOnly, MiddleSpacer: " "},
		InheritanceTraversal:      TraversalDepthFirstPreOrder,
		PostTraversalSort:         false,
		ContextSatisfyMode:        AtLeastOneValuePerKey,
		DisabledContexts:          map[string]struct{}{},
		IncludeGlobal:             true,
		IncludeGlobalWorld:        true,
		ApplyGlobalGroups:         true,
		ApplyGlobalWorldGroups:    true,
		MetaValueSelectionDefault: MetaSelectInheritance,
		MetaValueSelectionPerKey:  map[string]MetaValueSelection{},
		ApplyWildcards:            true,
		ApplySpongeWildcards:      false,
		ApplyDefaultNegBeforeWild: false,
		ApplyRegex:                true,
		ApplyShorthand:            true,
		ApplyChildPermissions:     true,
		ApplyDefaultPermissions:   true,
		WorldRewrites:             map[string]string{},
		GroupWeights:              map[string]int{},
		EnableOps:                 true,
		AutoOp:                    false,
		CommandsAllowOp:           true,
		BytebinURL:                "https://usercontent.luckperms.net/",
		BytesocksURL:              "https://usersockets.luckperms.net/",
		WebEditorURL:              "https://luckperms.net/editor/",
		VerboseViewerURL:          "https://luckperms.net/verbose/",
		TreeViewerURL:             "https://luckperms.net/treeview/",
	}
}

// IsContextDisabled reports whether contextKey is in disabled-contexts.
func (c *Config) IsContextDisabled(contextKey string) bool {
	_, disabled := c.DisabledContexts[strings.ToLower(contextKey)]
	return disabled
}

// MetaSelectionFor returns the MetaValueSelection strategy for metaKey.
func (c *Config) MetaSelectionFor(metaKey string) MetaValueSelection {
	if sel, ok := c.MetaValueSelectionPerKey[strings.ToLower(metaKey)]; ok {
		return sel
	}
	return c.MetaValueSelectionDefault
}

type rawConfig struct {
	Server             *string `yaml:"server"`
	UseServerUUIDCache *bool   `yaml:"use-server-uuid-cache"`
	StorageMethod      *string `yaml:"storage-method"`

	Data *struct {
		Address               *string `yaml:"address"`
		Database              *string `yaml:"database"`
		Username              *string `yaml:"username"`
		Password              *string `yaml:"password"`
		TablePrefix           *string `yaml:"table-prefix"`
		MongoCollectionPrefix *string `yaml:"mongodb-collection-prefix"`
		MongoConnectionURI    *string `yaml:"mongodb-connection-uri"`
		PoolSettings          *struct {
			MaxPoolSize       *int              `yaml:"maximum-pool-size"`
			MinIdle           *int              `yaml:"minimum-idle"`
			MaxLifetime       *int              `yaml:"maximum-lifetime"`
			ConnectionTimeout *int              `yaml:"connection-timeout"`
			Properties        map[string]string `yaml:"properties"`
		} `yaml:"pool-settings"`
	} `yaml:"data"`

	SplitStorage *struct {
		Enabled *bool `yaml:"enabled"`
		Methods *struct {
			User  *string `yaml:"user"`
			Group *string `yaml:"group"`
			Track *string `yaml:"track"`
			UUID  *string `yaml:"uuid"`
			Log   *string `yaml:"log"`
		} `yaml:"methods"`
	} `yaml:"split-storage"`

	SyncMinutes *int  `yaml:"sync-minutes"`
	WatchFiles  *bool `yaml:"watch-files"`

	MessagingService            *string `yaml:"messaging-service"`
	AutoPushUpdates             *bool   `yaml:"auto-push-updates"`
	PushLogEntries              *bool   `yaml:"push-log-entries"`
	BroadcastReceivedLogEntries *bool   `yaml:"broadcast-received-log-entries"`

	Redis *struct {
		Enabled   *bool    `yaml:"enabled"`
		Address   *string  `yaml:"address"`
		Addresses []string `yaml:"addresses"`
		Username  *string  `yaml:"username"`
		Password  *string  `yaml:"password"`
		SSL       *bool    `yaml:"ssl"`
		Sentinel  *struct {
			Enabled   *bool    `yaml:"enabled"`
			Master    *string  `yaml:"master"`
			Addresses []string `yaml:"addresses"`
			Username  *string  `yaml:"username"`
			Password  *string  `yaml:"password"`
		} `yaml:"sentinel"`
	} `yaml:"redis"`

	Nats *struct {
		Enabled  *bool   `yaml:"enabled"`
		Address  *string `yaml:"address"`
		Username *string `yaml:"username"`
		Password *string `yaml:"password"`
		Token    *string `yaml:"token"`
		SSL      *bool   `yaml:"ssl"`
	} `yaml:"nats"`

	RabbitMQ *struct {
		Enabled  *bool   `yaml:"enabled"`
		Address  *string `yaml:"address"`
		VHost    *string `yaml:"vhost"`
		Username *string `yaml:"username"`
		Password *string `yaml:"password"`
	} `yaml:"rabbitmq"`

	TemporaryAddBehaviour *string `yaml:"temporary-add-behaviour"`
	PrimaryGroupCalc      *string `yaml:"primary-group-calculation"`

	ArgumentBasedCommandPerms         *bool `yaml:"argument-based-command-permissions"`
	RequireSenderGroupMembershipToMod *bool `yaml:"require-sender-group-membership-to-modify"`

	LogNotify               *bool    `yaml:"log-notify"`
	LogNotifyFiltered       []string `yaml:"log-notify-filtered-descriptions"`
	AutoInstallTranslations *bool    `yaml:"auto-install-translations"`

	MetaFormatting *struct {
		Prefix *rawMetaStack `yaml:"prefix"`
		Suffix *rawMetaStack `yaml:"suffix"`
	} `yaml:"meta-formatting"`

	InheritanceTraversal      *string           `yaml:"inheritance-traversal-algorithm"`
	PostTraversalSort         *bool             `yaml:"post-traversal-inheritance-sort"`
	ContextSatisfyMode        *string           `yaml:"context-satisfy-mode"`
	DisabledContexts          []string          `yaml:"disabled-contexts"`
	IncludeGlobal             *bool             `yaml:"include-global"`
	IncludeGlobalWorld        *bool             `yaml:"include-global-world"`
	ApplyGlobalGroups         *bool             `yaml:"apply-global-groups"`
	ApplyGlobalWorldGroups    *bool             `yaml:"apply-global-world-groups"`
	MetaValueSelectionDefault *string           `yaml:"meta-value-selection-default"`
	MetaValueSelection        map[string]string `yaml:"meta-value-selection"`
	ApplyWildcards            *bool             `yaml:"apply-wildcards"`
	ApplySpongeWildcards      *bool             `yaml:"apply-sponge-implicit-wildcards"`
	ApplyDefaultNegBeforeWild *bool             `yaml:"apply-default-negated-permissions-before-wildcards"`
	ApplyRegex                *bool             `yaml:"apply-regex"`
	ApplyShorthand            *bool             `yaml:"apply-shorthand"`
	ApplyChildPermissions     *bool             `yaml:"apply-bukkit-child-permissions"`
	ApplyDefaultPermissions   *bool             `yaml:"apply-bukkit-default-permissions"`
	WorldRewrite              map[string]string `yaml:"world-rewrite"`
	GroupWeight               map[string]int    `yaml:"group-weight"`

	EnableOps       *bool `yaml:"enable-ops"`
	AutoOp          *bool `yaml:"auto-op"`
	CommandsAllowOp *bool `yaml:"commands-allow-op"`

	DebugLogins                *bool `yaml:"debug-logins"`
	AllowInvalidUsernames      *bool `yaml:"allow-invalid-usernames"`
	SkipBulkUpdateConfirmation *bool `yaml:"skip-bulkupdate-confirmation"`
	DisableBulkUpdate          *bool `yaml:"disable-bulkupdate"`
	PreventPrimaryGroupRemoval *bool `yaml:"prevent-primary-group-removal"`
	ResolveCommandSelectors    *bool `yaml:"resolve-command-selectors"`
	CommandsRateLimit          *bool `yaml:"commands-rate-limit"`

	CommandsReadOnlyMode *struct {
		Players *bool `yaml:"players"`
		Console *bool `yaml:"console"`
	} `yaml:"commands-read-only-mode"`

	DisableLuckPermsCommands *struct {
		Players *bool `yaml:"players"`
		Console *bool `yaml:"console"`
	} `yaml:"disable-luckperms-commands"`

	BytebinURL       *string `yaml:"bytebin-url"`
	BytesocksURL     *string `yaml:"bytesocks-url"`
	WebEditorURL     *string `yaml:"web-editor-url"`
	VerboseViewerURL *string `yaml:"verbose-viewer-url"`
	TreeViewerURL    *string `yaml:"tree-viewer-url"`
}

type rawMetaStack struct {
	Format       []string `yaml:"format"`
	Duplicates   *string  `yaml:"duplicates"`
	StartSpacer  *string  `yaml:"start-spacer"`
	MiddleSpacer *string  `yaml:"middle-spacer"`
	EndSpacer    *string  `yaml:"end-spacer"`
}

// LoadConfig reads config.yml (writing the default if missing) and optional contexts.json from dataDir.
func LoadConfig(dataDir string) (*Config, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(dataDir, "config.yml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath, defaultConfigYML, 0o644); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseConfigBytes(data)
	if err != nil {
		return nil, err
	}

	ctxPath := filepath.Join(dataDir, "contexts.json")
	if ctxData, err := os.ReadFile(ctxPath); err == nil {
		var cf struct {
			StaticContexts  map[string]any `json:"static-contexts"`
			DefaultContexts map[string]any `json:"default-contexts"`
		}
		if json.Unmarshal(ctxData, &cf) == nil {
			cfg.StaticContexts = ContextSetFromJSONMap(cf.StaticContexts)
			cfg.DefaultContexts = ContextSetFromJSONMap(cf.DefaultContexts)
		}
	}
	return cfg, nil
}

// ParseConfigBytes parses YAML config bytes into a Config.
func ParseConfigBytes(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Server != nil {
		cfg.Server = strings.ToLower(strings.TrimSpace(*raw.Server))
		if cfg.Server == "" {
			cfg.Server = GlobalContextValue
		}
	}
	if raw.UseServerUUIDCache != nil {
		cfg.UseServerUUIDCache = *raw.UseServerUUIDCache
	}
	if raw.StorageMethod != nil {
		cfg.StorageMethod = strings.ToLower(strings.TrimSpace(*raw.StorageMethod))
	}
	if raw.Data != nil {
		if raw.Data.Address != nil {
			cfg.Data.Address = strings.TrimSpace(*raw.Data.Address)
		}
		if raw.Data.Database != nil {
			cfg.Data.Database = strings.TrimSpace(*raw.Data.Database)
		}
		if raw.Data.Username != nil {
			cfg.Data.Username = *raw.Data.Username
		}
		if raw.Data.Password != nil {
			cfg.Data.Password = *raw.Data.Password
		}
		if raw.Data.TablePrefix != nil {
			cfg.Data.TablePrefix = *raw.Data.TablePrefix
		}
		if raw.Data.MongoCollectionPrefix != nil {
			cfg.Data.MongoCollectionPrefix = *raw.Data.MongoCollectionPrefix
		}
		if raw.Data.MongoConnectionURI != nil {
			cfg.Data.MongoConnectionURI = strings.TrimSpace(*raw.Data.MongoConnectionURI)
		}
		if ps := raw.Data.PoolSettings; ps != nil {
			if ps.MaxPoolSize != nil && *ps.MaxPoolSize > 0 {
				cfg.Data.MaxPoolSize = *ps.MaxPoolSize
			}
			if ps.MinIdle != nil && *ps.MinIdle >= 0 {
				cfg.Data.MinIdle = *ps.MinIdle
			}
			if ps.MaxLifetime != nil && *ps.MaxLifetime > 0 {
				cfg.Data.MaxLifetimeMillis = *ps.MaxLifetime
			}
			if ps.ConnectionTimeout != nil && *ps.ConnectionTimeout > 0 {
				cfg.Data.ConnectionTimeoutMs = *ps.ConnectionTimeout
			}
			for k, v := range ps.Properties {
				cfg.Data.Properties[k] = v
			}
		}
	}
	if raw.SplitStorage != nil {
		if raw.SplitStorage.Enabled != nil {
			cfg.SplitStorage.Enabled = *raw.SplitStorage.Enabled
		}
		if m := raw.SplitStorage.Methods; m != nil {
			if m.User != nil {
				cfg.SplitStorage.User = strings.ToLower(strings.TrimSpace(*m.User))
			}
			if m.Group != nil {
				cfg.SplitStorage.Group = strings.ToLower(strings.TrimSpace(*m.Group))
			}
			if m.Track != nil {
				cfg.SplitStorage.Track = strings.ToLower(strings.TrimSpace(*m.Track))
			}
			if m.UUID != nil {
				cfg.SplitStorage.UUID = strings.ToLower(strings.TrimSpace(*m.UUID))
			}
			if m.Log != nil {
				cfg.SplitStorage.Log = strings.ToLower(strings.TrimSpace(*m.Log))
			}
		}
	}
	if raw.SyncMinutes != nil {
		cfg.SyncMinutes = *raw.SyncMinutes
	}
	if raw.WatchFiles != nil {
		cfg.WatchFiles = *raw.WatchFiles
	}
	if raw.MessagingService != nil {
		cfg.MessagingService = strings.ToLower(strings.TrimSpace(*raw.MessagingService))
	}
	if raw.AutoPushUpdates != nil {
		cfg.AutoPushUpdates = *raw.AutoPushUpdates
	}
	if raw.PushLogEntries != nil {
		cfg.PushLogEntries = *raw.PushLogEntries
	}
	if raw.BroadcastReceivedLogEntries != nil {
		cfg.BroadcastReceivedLogEntries = *raw.BroadcastReceivedLogEntries
	}
	if raw.Redis != nil {
		if raw.Redis.Enabled != nil {
			cfg.Redis.Enabled = *raw.Redis.Enabled
		}
		if raw.Redis.Address != nil {
			cfg.Redis.Address = strings.TrimSpace(*raw.Redis.Address)
		}
		if len(raw.Redis.Addresses) > 0 {
			cfg.Redis.Addresses = slices.Clone(raw.Redis.Addresses)
		}
		if raw.Redis.Username != nil {
			cfg.Redis.Username = *raw.Redis.Username
		}
		if raw.Redis.Password != nil {
			cfg.Redis.Password = *raw.Redis.Password
		}
		if raw.Redis.SSL != nil {
			cfg.Redis.SSL = *raw.Redis.SSL
		}
		if s := raw.Redis.Sentinel; s != nil {
			if s.Enabled != nil {
				cfg.Redis.SentinelEnabled = *s.Enabled
			}
			if s.Master != nil {
				cfg.Redis.SentinelMaster = *s.Master
			}
			if len(s.Addresses) > 0 {
				cfg.Redis.SentinelAddrs = slices.Clone(s.Addresses)
			}
			if s.Username != nil {
				cfg.Redis.SentinelUsername = *s.Username
			}
			if s.Password != nil {
				cfg.Redis.SentinelPassword = *s.Password
			}
		}
	}
	if raw.Nats != nil {
		if raw.Nats.Enabled != nil {
			cfg.Nats.Enabled = *raw.Nats.Enabled
		}
		if raw.Nats.Address != nil {
			cfg.Nats.Address = strings.TrimSpace(*raw.Nats.Address)
		}
		if raw.Nats.Username != nil {
			cfg.Nats.Username = *raw.Nats.Username
		}
		if raw.Nats.Password != nil {
			cfg.Nats.Password = *raw.Nats.Password
		}
		if raw.Nats.Token != nil {
			cfg.Nats.Token = *raw.Nats.Token
		}
		if raw.Nats.SSL != nil {
			cfg.Nats.SSL = *raw.Nats.SSL
		}
	}
	if raw.RabbitMQ != nil {
		if raw.RabbitMQ.Enabled != nil {
			cfg.RabbitMQ.Enabled = *raw.RabbitMQ.Enabled
		}
		if raw.RabbitMQ.Address != nil {
			cfg.RabbitMQ.Address = strings.TrimSpace(*raw.RabbitMQ.Address)
		}
		if raw.RabbitMQ.VHost != nil {
			cfg.RabbitMQ.VHost = *raw.RabbitMQ.VHost
		}
		if raw.RabbitMQ.Username != nil {
			cfg.RabbitMQ.Username = *raw.RabbitMQ.Username
		}
		if raw.RabbitMQ.Password != nil {
			cfg.RabbitMQ.Password = *raw.RabbitMQ.Password
		}
	}
	if raw.TemporaryAddBehaviour != nil {
		switch strings.ToLower(strings.TrimSpace(*raw.TemporaryAddBehaviour)) {
		case "accumulate":
			cfg.TemporaryAddBehaviour = TempMergeAccumulate
		case "replace":
			cfg.TemporaryAddBehaviour = TempMergeReplace
		default:
			cfg.TemporaryAddBehaviour = TempMergeDeny
		}
	}
	if raw.PrimaryGroupCalc != nil {
		switch strings.ToLower(strings.TrimSpace(*raw.PrimaryGroupCalc)) {
		case "stored":
			cfg.PrimaryGroupCalc = PrimaryGroupStored
		case "all-parents-by-weight":
			cfg.PrimaryGroupCalc = PrimaryGroupAllParentsByWeight
		default:
			cfg.PrimaryGroupCalc = PrimaryGroupParentsByWeight
		}
	}
	if raw.ArgumentBasedCommandPerms != nil {
		cfg.ArgumentBasedCommandPerms = *raw.ArgumentBasedCommandPerms
	}
	if raw.RequireSenderGroupMembershipToMod != nil {
		cfg.RequireSenderGroupMembershipToMod = *raw.RequireSenderGroupMembershipToMod
	}
	if raw.LogNotify != nil {
		cfg.LogNotify = *raw.LogNotify
	}
	for _, pat := range raw.LogNotifyFiltered {
		if rx, err := regexp.Compile(pat); err == nil {
			cfg.LogNotifyFilterRegex = append(cfg.LogNotifyFilterRegex, rx)
		}
	}
	if raw.AutoInstallTranslations != nil {
		cfg.AutoInstallTranslations = *raw.AutoInstallTranslations
	}
	if raw.MetaFormatting != nil {
		if raw.MetaFormatting.Prefix != nil {
			applyRawMetaStack(&cfg.PrefixFormatting, raw.MetaFormatting.Prefix)
		}
		if raw.MetaFormatting.Suffix != nil {
			applyRawMetaStack(&cfg.SuffixFormatting, raw.MetaFormatting.Suffix)
		}
	}
	if raw.InheritanceTraversal != nil {
		switch strings.ToLower(strings.TrimSpace(*raw.InheritanceTraversal)) {
		case "breadth-first":
			cfg.InheritanceTraversal = TraversalBreadthFirst
		case "depth-first-post-order":
			cfg.InheritanceTraversal = TraversalDepthFirstPostOrder
		default:
			cfg.InheritanceTraversal = TraversalDepthFirstPreOrder
		}
	}
	if raw.PostTraversalSort != nil {
		cfg.PostTraversalSort = *raw.PostTraversalSort
	}
	if raw.ContextSatisfyMode != nil {
		if strings.EqualFold(strings.TrimSpace(*raw.ContextSatisfyMode), "all-values-per-key") {
			cfg.ContextSatisfyMode = AllValuesPerKey
		} else {
			cfg.ContextSatisfyMode = AtLeastOneValuePerKey
		}
	}
	for _, dc := range raw.DisabledContexts {
		cfg.DisabledContexts[strings.ToLower(strings.TrimSpace(dc))] = struct{}{}
	}
	if raw.IncludeGlobal != nil {
		cfg.IncludeGlobal = *raw.IncludeGlobal
	}
	if raw.IncludeGlobalWorld != nil {
		cfg.IncludeGlobalWorld = *raw.IncludeGlobalWorld
	}
	if raw.ApplyGlobalGroups != nil {
		cfg.ApplyGlobalGroups = *raw.ApplyGlobalGroups
	}
	if raw.ApplyGlobalWorldGroups != nil {
		cfg.ApplyGlobalWorldGroups = *raw.ApplyGlobalWorldGroups
	}
	if raw.MetaValueSelectionDefault != nil {
		cfg.MetaValueSelectionDefault = parseMetaSel(*raw.MetaValueSelectionDefault)
	}
	for k, v := range raw.MetaValueSelection {
		cfg.MetaValueSelectionPerKey[strings.ToLower(k)] = parseMetaSel(v)
	}
	if raw.ApplyWildcards != nil {
		cfg.ApplyWildcards = *raw.ApplyWildcards
	}
	if raw.ApplySpongeWildcards != nil {
		cfg.ApplySpongeWildcards = *raw.ApplySpongeWildcards
	}
	if raw.ApplyDefaultNegBeforeWild != nil {
		cfg.ApplyDefaultNegBeforeWild = *raw.ApplyDefaultNegBeforeWild
	}
	if raw.ApplyRegex != nil {
		cfg.ApplyRegex = *raw.ApplyRegex
	}
	if raw.ApplyShorthand != nil {
		cfg.ApplyShorthand = *raw.ApplyShorthand
	}
	if raw.ApplyChildPermissions != nil {
		cfg.ApplyChildPermissions = *raw.ApplyChildPermissions
	}
	if raw.ApplyDefaultPermissions != nil {
		cfg.ApplyDefaultPermissions = *raw.ApplyDefaultPermissions
	}
	for k, v := range raw.WorldRewrite {
		cfg.WorldRewrites[strings.ToLower(k)] = strings.ToLower(v)
	}
	for k, v := range raw.GroupWeight {
		cfg.GroupWeights[strings.ToLower(k)] = v
	}
	if raw.EnableOps != nil {
		cfg.EnableOps = *raw.EnableOps
	}
	if raw.AutoOp != nil {
		cfg.AutoOp = *raw.AutoOp
	}
	if cfg.AutoOp {
		cfg.EnableOps = false
	}
	if raw.CommandsAllowOp != nil {
		cfg.CommandsAllowOp = *raw.CommandsAllowOp
	}
	if raw.DebugLogins != nil {
		cfg.DebugLogins = *raw.DebugLogins
	}
	if raw.AllowInvalidUsernames != nil {
		cfg.AllowInvalidUsernames = *raw.AllowInvalidUsernames
	}
	if raw.SkipBulkUpdateConfirmation != nil {
		cfg.SkipBulkUpdateConfirmation = *raw.SkipBulkUpdateConfirmation
	}
	if raw.DisableBulkUpdate != nil {
		cfg.DisableBulkUpdate = *raw.DisableBulkUpdate
	}
	if raw.PreventPrimaryGroupRemoval != nil {
		cfg.PreventPrimaryGroupRemoval = *raw.PreventPrimaryGroupRemoval
	}
	if raw.ResolveCommandSelectors != nil {
		cfg.ResolveCommandSelectors = *raw.ResolveCommandSelectors
	}
	if raw.CommandsRateLimit != nil {
		cfg.CommandsRateLimit = *raw.CommandsRateLimit
	}
	if raw.CommandsReadOnlyMode != nil {
		if raw.CommandsReadOnlyMode.Players != nil {
			cfg.ReadOnlyPlayers = *raw.CommandsReadOnlyMode.Players
		}
		if raw.CommandsReadOnlyMode.Console != nil {
			cfg.ReadOnlyConsole = *raw.CommandsReadOnlyMode.Console
		}
	}
	if raw.DisableLuckPermsCommands != nil {
		if raw.DisableLuckPermsCommands.Players != nil {
			cfg.DisablePlayers = *raw.DisableLuckPermsCommands.Players
		}
		if raw.DisableLuckPermsCommands.Console != nil {
			cfg.DisableConsole = *raw.DisableLuckPermsCommands.Console
		}
	}
	if raw.BytebinURL != nil && *raw.BytebinURL != "" {
		cfg.BytebinURL = ensureTrailingSlash(*raw.BytebinURL)
	}
	if raw.BytesocksURL != nil && *raw.BytesocksURL != "" {
		cfg.BytesocksURL = ensureTrailingSlash(*raw.BytesocksURL)
	}
	if raw.WebEditorURL != nil && *raw.WebEditorURL != "" {
		cfg.WebEditorURL = ensureTrailingSlash(*raw.WebEditorURL)
	}
	if raw.VerboseViewerURL != nil && *raw.VerboseViewerURL != "" {
		cfg.VerboseViewerURL = ensureTrailingSlash(*raw.VerboseViewerURL)
	}
	if raw.TreeViewerURL != nil && *raw.TreeViewerURL != "" {
		cfg.TreeViewerURL = ensureTrailingSlash(*raw.TreeViewerURL)
	}
	return cfg, nil
}

func ensureTrailingSlash(u string) string {
	if strings.HasSuffix(u, "/") {
		return u
	}
	return u + "/"
}

func applyRawMetaStack(dst *MetaStackConfig, src *rawMetaStack) {
	if len(src.Format) > 0 {
		dst.Format = slices.Clone(src.Format)
	}
	if src.Duplicates != nil {
		switch strings.ToLower(strings.TrimSpace(*src.Duplicates)) {
		case "retain-all":
			dst.Duplicates = DuplicateRetainAll
		case "last-only":
			dst.Duplicates = DuplicateLastOnly
		default:
			dst.Duplicates = DuplicateFirstOnly
		}
	}
	if src.StartSpacer != nil {
		dst.StartSpacer = *src.StartSpacer
	}
	if src.MiddleSpacer != nil {
		dst.MiddleSpacer = *src.MiddleSpacer
	}
	if src.EndSpacer != nil {
		dst.EndSpacer = *src.EndSpacer
	}
}

func parseMetaSel(s string) MetaValueSelection {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "highest-number":
		return MetaSelectHighestNumber
	case "lowest-number":
		return MetaSelectLowestNumber
	default:
		return MetaSelectInheritance
	}
}
