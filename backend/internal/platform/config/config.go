// Package config 读取并校验运行配置。
//
// 配置分三层：结构体默认值 → YAML 文件 → DEVMASTER_* 环境变量。
// 环境变量优先级最高，因为容器/CI 里改一个值不该要求改文件。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

type Config struct {
	Environment  string                `yaml:"environment"`
	Server       ServerConfig          `yaml:"server"`
	Database     DatabaseConfig        `yaml:"database"`
	Logging      LoggingConfig         `yaml:"logging"`
	Auth         AuthConfig            `yaml:"auth"`
	NATS         NATSConfig            `yaml:"nats"`
	Temporal     TemporalConfig        `yaml:"temporal"`
	Outbox       OutboxConfig          `yaml:"outbox"`
	Approver     ApproverConfig        `yaml:"approver"`
	Gitea        GiteaConfig           `yaml:"gitea"`
	Notify       NotifyConfig          `yaml:"notify"`
	Artifact     ArtifactConfig        `yaml:"artifact"`
	Subjects     SubjectRegistryConfig `yaml:"subject_registry"`
	Integrations IntegrationConfig     `yaml:"integrations"`
}

type ServerConfig struct {
	GRPCAddress     string        `yaml:"grpc_address"`
	HTTPAddress     string        `yaml:"http_address"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	HealthTimeout   time.Duration `yaml:"health_timeout"`
}

type DatabaseConfig struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	Name            string        `yaml:"name"`
	User            string        `yaml:"user"`
	Password        string        `yaml:"password"`
	SSLMode         string        `yaml:"ssl_mode"`
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // text / json
}

type AuthConfig struct {
	// Enabled=false 只允许在 development/test 下使用：拦截器注入 demo 主体。
	// 生产环境启动即拒绝 —— 否则"忘记开鉴权"会变成一个静默的越权入口。
	Enabled            bool     `yaml:"enabled"`
	Issuer             string   `yaml:"issuer"`
	Audience           string   `yaml:"audience"`
	DemoSubject        string   `yaml:"demo_subject"`
	DemoOrganizationID string   `yaml:"demo_organization_id"`
	DemoRoles          []string `yaml:"demo_roles"`
}

type NATSConfig struct {
	URL           string `yaml:"url"`
	Stream        string `yaml:"stream"`
	SubjectPrefix string `yaml:"subject_prefix"`
	// ReadOnly 表示流不归本服务管（只校验存在，不改写配置）。
	ReadOnly bool `yaml:"read_only"`
}

type TemporalConfig struct {
	HostPort  string `yaml:"host_port"`
	Namespace string `yaml:"namespace"`
	TaskQueue string `yaml:"task_queue"`
}

type OutboxConfig struct {
	BatchSize    int           `yaml:"batch_size"`
	PollInterval time.Duration `yaml:"poll_interval"`
	MaxAttempts  int           `yaml:"max_attempts"`
	BaseBackoff  time.Duration `yaml:"base_backoff"`
	MaxBackoff   time.Duration `yaml:"max_backoff"`
}

type ApproverConfig struct {
	BaseURL      string        `yaml:"base_url"`
	TenantID     string        `yaml:"tenant_id"`
	ServiceUser  string        `yaml:"service_user"`
	ApproverUser string        `yaml:"approver_user"`
	ProcessKey   string        `yaml:"process_key"`
	PollInterval time.Duration `yaml:"poll_interval"`
	PollTimeout  time.Duration `yaml:"poll_timeout"`
}

type GiteaConfig struct {
	BaseURL       string `yaml:"base_url"`
	Token         string `yaml:"token"`
	Owner         string `yaml:"owner"`
	Repo          string `yaml:"repo"`
	WebhookSecret string `yaml:"webhook_secret"`
	// OrganizationID 是 webhook 的归属组织：Gitea 的 payload 不带租户信息。
	OrganizationID string `yaml:"organization_id"`
}

type NotifyConfig struct {
	Enabled     bool   `yaml:"enabled"`
	NATSURL     string `yaml:"nats_url"`
	Tenant      string `yaml:"tenant"`
	RecipientID string `yaml:"recipient_id"`
	// ApprovalReminderAfter 是待审批催办阈值：审批等待超过它就发一次提醒。
	// 必须小于 approver.poll_timeout，否则到点先超时、提醒永远发不出来。
	ApprovalReminderAfter time.Duration `yaml:"approval_reminder_after"`
}

// ArtifactConfig 是 artifact（内容与证据仓）的接入参数。
//
// Enabled=false 时发布链跳过存证并把 evidence_ref 留空——这是本地没有
// artifact 时的显式降级，不是默认值：生产必须开启（Validate 会拒）。
type ArtifactConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Address          string        `yaml:"address"`
	Issuer           string        `yaml:"issuer"`
	TokenURL         string        `yaml:"token_url"`
	ClientID         string        `yaml:"client_id"`
	ClientSecret     string        `yaml:"client_secret"`
	TenantID         string        `yaml:"tenant_id"`
	OrganizationID   string        `yaml:"organization_id"`
	Namespace        string        `yaml:"namespace"`
	TestReportType   string        `yaml:"test_report_type"`
	ReleaseNotesType string        `yaml:"release_notes_type"`
	Timeout          time.Duration `yaml:"timeout"`
	TLS              bool          `yaml:"tls"`
}

// SubjectRegistryConfig 是主体目录（subject-registry）的接入参数。
//
// 与 artifact 不同，这里 enabled=false 不需要在生产拒绝启动：主体目录只影响
// 「催办发给谁」这个路由优化，未接入时降级到角色收件箱，通知照发。把它设成
// 生产必需项会把一个旁路依赖变成发布链的单点。
type SubjectRegistryConfig struct {
	Enabled      bool          `yaml:"enabled"`
	Address      string        `yaml:"address"`
	ServiceToken string        `yaml:"service_token"`
	Timeout      time.Duration `yaml:"timeout"`
}

type IntegrationConfig struct {
	// BFFSharedSecret 是 Next.js BFF 的签名密钥。M0 的只读控制台面不经过它，
	// 但保留配置位，M1 接入写操作时不再改配置结构。
	BFFSharedSecret string `yaml:"bff_shared_secret"`
}

// Default 返回一套可本地直接跑起来的默认值。
func Default() Config {
	return Config{
		Environment: EnvDevelopment,
		Server: ServerConfig{
			GRPCAddress:     "127.0.0.1:50091",
			HTTPAddress:     "127.0.0.1:8291",
			ShutdownTimeout: 15 * time.Second,
			HealthTimeout:   2 * time.Second,
		},
		Database: DatabaseConfig{
			Host:            "127.0.0.1",
			Port:            5432,
			Name:            "devmaster_dev",
			User:            "devmaster_app",
			SSLMode:         "disable",
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Logging: LoggingConfig{Level: "info", Format: "text"},
		Auth: AuthConfig{
			Enabled:            false,
			Issuer:             "http://127.0.0.1:8180/realms/devmaster",
			Audience:           "devmaster-service",
			DemoSubject:        "local-dev",
			DemoOrganizationID: "00000000-0000-0000-0000-000000000001",
			DemoRoles:          []string{"DEV_ADMIN", "DEV_LEAD", "DEV_MEMBER", "DEV_VIEWER", "RELEASE_APPROVER"},
		},
		NATS: NATSConfig{
			URL:           "nats://127.0.0.1:4222",
			Stream:        "DEVMASTER_EVENTS",
			SubjectPrefix: "dev",
			ReadOnly:      true,
		},
		Temporal: TemporalConfig{
			HostPort:  "127.0.0.1:7233",
			Namespace: "devmaster",
			TaskQueue: "devmaster-maintenance",
		},
		Outbox: OutboxConfig{
			BatchSize:    100,
			PollInterval: 1 * time.Second,
			MaxAttempts:  10,
			BaseBackoff:  2 * time.Second,
			MaxBackoff:   5 * time.Minute,
		},
		Approver: ApproverConfig{
			BaseURL:      "http://127.0.0.1:18090",
			TenantID:     "default",
			ServiceUser:  "devmaster-service",
			ApproverUser: "release-approver",
			ProcessKey:   "devmaster-release-approval",
			PollInterval: 5 * time.Second,
			PollTimeout:  30 * time.Minute,
		},
		Notify: NotifyConfig{
			Enabled:               false,
			NATSURL:               "nats://127.0.0.1:4222",
			Tenant:                "devmaster",
			RecipientID:           "team:devmaster",
			ApprovalReminderAfter: time.Hour,
		},
		Gitea: GiteaConfig{
			BaseURL:        "http://127.0.0.1:3123",
			Owner:          "meshadmin",
			Repo:           "hosp",
			OrganizationID: "00000000-0000-0000-0000-000000000001",
		},
		Artifact: ArtifactConfig{
			Enabled:          false,
			Address:          "127.0.0.1:50071",
			Issuer:           "http://127.0.0.1:8180/realms/artifact",
			ClientID:         "content-worker",
			TenantID:         "00000000-0000-0000-0000-000000000001",
			OrganizationID:   "00000000-0000-0000-0000-000000000001",
			Namespace:        "devmaster",
			TestReportType:   "devmaster.test-report",
			ReleaseNotesType: "devmaster.release-notes",
			Timeout:          30 * time.Second,
		},
		Subjects: SubjectRegistryConfig{
			Enabled: false,
			Address: "127.0.0.1:50095",
			Timeout: 5 * time.Second,
		},
	}
}

// Load 读取 YAML（可选）并叠加环境变量，最后做一次校验。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	setString(&cfg.Environment, "DEVMASTER_ENV")
	setString(&cfg.Server.GRPCAddress, "DEVMASTER_GRPC_ADDRESS")
	setString(&cfg.Server.HTTPAddress, "DEVMASTER_HTTP_ADDRESS")
	setString(&cfg.Database.Host, "DEVMASTER_DATABASE_HOST")
	setInt(&cfg.Database.Port, "DEVMASTER_DATABASE_PORT")
	setString(&cfg.Database.Name, "DEVMASTER_DATABASE_NAME")
	setString(&cfg.Database.User, "DEVMASTER_DATABASE_USER")
	setString(&cfg.Database.Password, "DEVMASTER_DATABASE_PASSWORD")
	setString(&cfg.NATS.URL, "DEVMASTER_NATS_URL")
	setString(&cfg.NATS.Stream, "DEVMASTER_NATS_STREAM")
	setString(&cfg.Temporal.HostPort, "DEVMASTER_TEMPORAL_HOSTPORT")
	setString(&cfg.Temporal.Namespace, "DEVMASTER_TEMPORAL_NAMESPACE")
	setString(&cfg.Approver.BaseURL, "DEVMASTER_APPROVER_BASE_URL")
	setString(&cfg.Approver.ProcessKey, "DEVMASTER_APPROVER_PROCESS_KEY")
	setString(&cfg.Gitea.BaseURL, "DEVMASTER_GITEA_BASE_URL")
	setString(&cfg.Gitea.Token, "DEVMASTER_GITEA_TOKEN")
	setString(&cfg.Gitea.Owner, "DEVMASTER_GITEA_OWNER")
	setString(&cfg.Gitea.Repo, "DEVMASTER_GITEA_REPO")
	setString(&cfg.Gitea.WebhookSecret, "DEVMASTER_GITEA_WEBHOOK_SECRET")
	setString(&cfg.Gitea.OrganizationID, "DEVMASTER_GITEA_ORGANIZATION_ID")
	setBool(&cfg.Notify.Enabled, "DEVMASTER_NOTIFY_ENABLED")
	setString(&cfg.Notify.NATSURL, "DEVMASTER_NOTIFY_NATS_URL")
	setString(&cfg.Notify.Tenant, "DEVMASTER_NOTIFY_TENANT")
	setString(&cfg.Notify.RecipientID, "DEVMASTER_NOTIFY_RECIPIENT_ID")
	setDuration(&cfg.Notify.ApprovalReminderAfter, "DEVMASTER_NOTIFY_APPROVAL_REMINDER_AFTER")
	setBool(&cfg.Auth.Enabled, "DEVMASTER_AUTH_ENABLED")
	setString(&cfg.Auth.Issuer, "DEVMASTER_AUTH_ISSUER")
	setString(&cfg.Auth.Audience, "DEVMASTER_AUTH_AUDIENCE")
	setBool(&cfg.Artifact.Enabled, "DEVMASTER_ARTIFACT_ENABLED")
	setString(&cfg.Artifact.Address, "DEVMASTER_ARTIFACT_ADDRESS")
	setString(&cfg.Artifact.Issuer, "DEVMASTER_ARTIFACT_ISSUER")
	setString(&cfg.Artifact.TokenURL, "DEVMASTER_ARTIFACT_TOKEN_URL")
	setString(&cfg.Artifact.ClientID, "DEVMASTER_ARTIFACT_CLIENT_ID")
	setString(&cfg.Artifact.ClientSecret, "DEVMASTER_ARTIFACT_CLIENT_SECRET")
	setString(&cfg.Artifact.TenantID, "DEVMASTER_ARTIFACT_TENANT_ID")
	setString(&cfg.Artifact.OrganizationID, "DEVMASTER_ARTIFACT_ORGANIZATION_ID")
	setString(&cfg.Artifact.Namespace, "DEVMASTER_ARTIFACT_NAMESPACE")
	setBool(&cfg.Subjects.Enabled, "DEVMASTER_SUBJECT_REGISTRY_ENABLED")
	setString(&cfg.Subjects.Address, "DEVMASTER_SUBJECT_REGISTRY_ADDRESS")
	setString(&cfg.Subjects.ServiceToken, "DEVMASTER_SUBJECT_REGISTRY_TOKEN")
}

// Validate 是"生产拒启"的唯一入口。
func (c Config) Validate() error {
	switch c.Environment {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		return fmt.Errorf("unknown environment %q", c.Environment)
	}
	if c.Database.Name == "" || c.Database.User == "" {
		return errors.New("database.name and database.user are required")
	}
	if c.Outbox.BatchSize <= 0 || c.Outbox.MaxAttempts <= 0 {
		return errors.New("outbox.batch_size and outbox.max_attempts must be positive")
	}
	if c.Environment == EnvProduction {
		if !c.Auth.Enabled {
			return errors.New("auth.enabled must be true in production")
		}
		if strings.TrimSpace(c.Auth.Issuer) == "" || strings.TrimSpace(c.Auth.Audience) == "" {
			return errors.New("auth.issuer and auth.audience are required in production")
		}
		// 发布证据是审计事实：生产环境关掉存证等于发布链缺一环。
		if !c.Artifact.Enabled {
			return errors.New("artifact.enabled must be true in production")
		}
	}
	if c.Artifact.Enabled {
		if strings.TrimSpace(c.Artifact.Address) == "" ||
			strings.TrimSpace(c.Artifact.ClientID) == "" ||
			strings.TrimSpace(c.Artifact.ClientSecret) == "" {
			return errors.New("artifact.address and client credentials are required when artifact is enabled")
		}
		if strings.TrimSpace(c.Artifact.TokenURL) == "" && strings.TrimSpace(c.Artifact.Issuer) == "" {
			return errors.New("artifact.token_url or artifact.issuer is required when artifact is enabled")
		}
	}
	return nil
}

func (c Config) DSN() string { return c.Database.DSN() }

func (c DatabaseConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Name, c.SSLMode)
}

func setString(target *string, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		*target = value
	}
}

func setInt(target *int, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			*target = parsed
		}
	}
}

func setDuration(target *time.Duration, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			*target = parsed
		}
	}
}

func setBool(target *bool, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			*target = parsed
		}
	}
}
