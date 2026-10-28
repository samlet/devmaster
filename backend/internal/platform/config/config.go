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
	Environment  string            `yaml:"environment"`
	Server       ServerConfig      `yaml:"server"`
	Database     DatabaseConfig    `yaml:"database"`
	Logging      LoggingConfig     `yaml:"logging"`
	Auth         AuthConfig        `yaml:"auth"`
	NATS         NATSConfig        `yaml:"nats"`
	Temporal     TemporalConfig    `yaml:"temporal"`
	Outbox       OutboxConfig      `yaml:"outbox"`
	Approver     ApproverConfig    `yaml:"approver"`
	Gitea        GiteaConfig       `yaml:"gitea"`
	Integrations IntegrationConfig `yaml:"integrations"`
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
		Gitea: GiteaConfig{
			BaseURL:        "http://127.0.0.1:3123",
			Owner:          "meshadmin",
			Repo:           "hosp",
			OrganizationID: "00000000-0000-0000-0000-000000000001",
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
	setBool(&cfg.Auth.Enabled, "DEVMASTER_AUTH_ENABLED")
	setString(&cfg.Auth.Issuer, "DEVMASTER_AUTH_ISSUER")
	setString(&cfg.Auth.Audience, "DEVMASTER_AUTH_AUDIENCE")
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

func setBool(target *bool, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			*target = parsed
		}
	}
}
