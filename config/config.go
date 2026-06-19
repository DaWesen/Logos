package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

var (
	cfg     *Config
	cfgOnce sync.Once
)

// Config 应用配置结构体
type Config struct {
	Ports         Ports         `mapstructure:"ports"`
	Services      Services      `mapstructure:"services"`
	Database      Database      `mapstructure:"database"`
	Redis         Redis         `mapstructure:"redis"`
	Kafka         Kafka         `mapstructure:"kafka"`
	Elasticsearch Elasticsearch `mapstructure:"elasticsearch"`
	Neo4j         Neo4j         `mapstructure:"neo4j"`
	Milvus        Milvus        `mapstructure:"milvus"`
	Minio         Minio         `mapstructure:"minio"`
	Log           Log           `mapstructure:"log"`
	JWT           JWT           `mapstructure:"jwt"`
	Tracing       Tracing       `mapstructure:"tracing"`
	Etcd          Etcd          `mapstructure:"etcd"`
	GRPC          GRPC          `mapstructure:"grpc"`
	Eino          Eino          `mapstructure:"eino"`
	QQBridge      QQBridge      `mapstructure:"qqbridge"`
}

// Ports 服务端口配置
type Ports struct {
	Gateway    int `mapstructure:"gateway"`
	User       int `mapstructure:"user"`
	Knowledge  int `mapstructure:"knowledge"`
	Question   int `mapstructure:"question"`
	Recommend  int `mapstructure:"recommend"`
	Collection int `mapstructure:"collection"`
	Vector     int `mapstructure:"vector"`
	Search     int `mapstructure:"search"`
	Extraction int `mapstructure:"extraction"`
	Summary    int `mapstructure:"summary"`
	MCP        int `mapstructure:"mcp"`
	Moderation int `mapstructure:"moderation"`
	Message    int `mapstructure:"message"`
	Monitoring int `mapstructure:"monitoring"`
	IM         int `mapstructure:"im"`
	Chat       int `mapstructure:"chat"`
	Contact    int `mapstructure:"contact"`
	Bot        int `mapstructure:"bot"`
	Billing    int `mapstructure:"billing"`
	Process    int `mapstructure:"process"`
	QQBridge   int `mapstructure:"qqbridge"`
}

// ServiceConfig 单个服务配置
type ServiceConfig struct {
	Timeout string `mapstructure:"timeout"`
}

// Services 所有服务配置
type Services struct {
	User       ServiceConfig `mapstructure:"user"`
	Knowledge  ServiceConfig `mapstructure:"knowledge"`
	Question   ServiceConfig `mapstructure:"question"`
	Recommend  ServiceConfig `mapstructure:"recommend"`
	Collection ServiceConfig `mapstructure:"collection"`
	Vector     ServiceConfig `mapstructure:"vector"`
	Search     ServiceConfig `mapstructure:"search"`
	Extraction ServiceConfig `mapstructure:"extraction"`
	Summary    ServiceConfig `mapstructure:"summary"`
	MCP        ServiceConfig `mapstructure:"mcp"`
	Moderation ServiceConfig `mapstructure:"moderation"`
	Message    ServiceConfig `mapstructure:"message"`
	Monitoring ServiceConfig `mapstructure:"monitoring"`
	IM         ServiceConfig `mapstructure:"im"`
	Chat       ServiceConfig `mapstructure:"chat"`
	Contact    ServiceConfig `mapstructure:"contact"`
	Bot        ServiceConfig `mapstructure:"bot"`
	Billing    ServiceConfig `mapstructure:"billing"`
}

// Database 数据库配置
type Database struct {
	Postgres Postgres `mapstructure:"postgres"`
}

// PostgreSQL 配置
type Postgres struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	DBName          string `mapstructure:"dbname"`
	SSLMode         string `mapstructure:"sslmode"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

// Redis Redis配置
type Redis struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

// Kafka Kafka配置
type Kafka struct {
	Brokers []string          `mapstructure:"brokers"`
	Version string            `mapstructure:"version"`
	Topics  map[string]string `mapstructure:"topics"`
}

// Elasticsearch Elasticsearch配置
type Elasticsearch struct {
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// Neo4j Neo4j配置
type Neo4j struct {
	URI      string `mapstructure:"uri"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

// Milvus Milvus配置
type Milvus struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// Minio Minio配置
type Minio struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	Secure    bool   `mapstructure:"secure"`
}

// Log 日志配置
type Log struct {
	Level    string `mapstructure:"level"`
	Format   string `mapstructure:"format"`
	Output   string `mapstructure:"output"`
	FilePath string `mapstructure:"file_path"`
}

// JWT 配置
type JWT struct {
	Secret      string `mapstructure:"secret"`
	ExpireHours int    `mapstructure:"expire_hours"`
}

// Tracing 追踪配置（基于 OpenTelemetry）
type Tracing struct {
	Enable       bool    `mapstructure:"enable"`
	OtelEndpoint string  `mapstructure:"otel_endpoint"`
	SampleRate   float64 `mapstructure:"sample_rate"`
}

// Etcd 配置
type Etcd struct {
	Endpoints    []string `mapstructure:"endpoints"`
	DialTimeout  string   `mapstructure:"dial_timeout"`
	Username     string   `mapstructure:"username"`
	Password     string   `mapstructure:"password"`
	TTL          int      `mapstructure:"ttl"`
	EnableSecure bool     `mapstructure:"enable_secure"`
}

// GRPC gRPC配置
type GRPC struct {
	Client GRPCClient `mapstructure:"client"`
	Server GRPCServer `mapstructure:"server"`
}

// GRPCClient gRPC客户端配置
type GRPCClient struct {
	Timeout        string `mapstructure:"timeout"`
	ConnTimeout    string `mapstructure:"conn_timeout"`
	MaxRecvMsgSize int    `mapstructure:"max_recv_msg_size"`
	MaxSendMsgSize int    `mapstructure:"max_send_msg_size"`
}

// GRPCServer gRPC服务器配置
type GRPCServer struct {
	Timeout          string `mapstructure:"timeout"`
	MaxConns         int    `mapstructure:"max_conns"`
	MaxRecvMsgSize   int    `mapstructure:"max_recv_msg_size"`
	MaxSendMsgSize   int    `mapstructure:"max_send_msg_size"`
	KeepaliveTime    string `mapstructure:"keepalive_time"`
	KeepaliveTimeout string `mapstructure:"keepalive_timeout"`
}

// Eino Eino配置
type Eino struct {
	APIKey         string `mapstructure:"api_key"`
	Model          string `mapstructure:"model"`
	BaseURL        string `mapstructure:"base_url"`
	EmbeddingModel string `mapstructure:"embedding_model"`
}

// QQBridge QQ Bridge配置
type QQBridge struct {
	Enabled          bool   `mapstructure:"enabled"`
	WSHost           string `mapstructure:"ws_host"`
	WSPort           int    `mapstructure:"ws_port"`
	WSForwardURL     string `mapstructure:"ws_forward_url"`
	ConsumerGroup    string `mapstructure:"consumer_group"`
	OutgoingGroup    string `mapstructure:"outgoing_group"`
	QQUserPrefix     string `mapstructure:"qq_user_prefix"`
	QQGroupPrefix    string `mapstructure:"qq_group_prefix"`
	AutoRegisterUser bool   `mapstructure:"auto_register_user"`
	AutoCreateGroup  bool   `mapstructure:"auto_create_group"`
	TempDir          string `mapstructure:"temp_dir"`
}

func LoadConfig(path string) (*Config, error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	viper.AutomaticEnv()

	bindEnvVars()

	err := viper.ReadInConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	err = viper.Unmarshal(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	overrideFromEnv(&config)

	return &config, nil
}

func bindEnvVars() {
	envBindings := []struct {
		envKey string
		cfgKey string
	}{
		{"POSTGRES_HOST", "database.postgres.host"},
		{"POSTGRES_PORT", "database.postgres.port"},
		{"POSTGRES_USER", "database.postgres.user"},
		{"POSTGRES_PASSWORD", "database.postgres.password"},
		{"POSTGRES_DB", "database.postgres.dbname"},
		{"REDIS_HOST", "redis.host"},
		{"REDIS_PORT", "redis.port"},
		{"REDIS_PASSWORD", "redis.password"},
		{"ELASTICSEARCH_URL", "elasticsearch.url"},
		{"NEO4J_URI", "neo4j.uri"},
		{"NEO4J_USERNAME", "neo4j.user"},
		{"NEO4J_PASSWORD", "neo4j.password"},
		{"MILVUS_HOST", "milvus.host"},
		{"MINIO_ENDPOINT", "minio.endpoint"},
		{"MINIO_ACCESS_KEY", "minio.access_key"},
		{"MINIO_SECRET_KEY", "minio.secret_key"},
		{"ARK_API_KEY", "eino.api_key"},
		{"ARK_MODEL", "eino.model"},
		{"ARK_BASE_URL", "eino.base_url"},
		{"JWT_SECRET", "jwt.secret"},
		{"QQBRIDGE_ENABLED", "qqbridge.enabled"},
		{"QQBRIDGE_WS_HOST", "qqbridge.ws_host"},
		{"QQBRIDGE_WS_PORT", "qqbridge.ws_port"},
		{"QQBRIDGE_WS_FORWARD_URL", "qqbridge.ws_forward_url"},
	}

	for _, b := range envBindings {
		_ = viper.BindEnv(b.cfgKey, b.envKey)
	}
}

func overrideFromEnv(cfg *Config) {
	if v := os.Getenv("POSTGRES_HOST"); v != "" {
		cfg.Database.Postgres.Host = v
	}
	if v := os.Getenv("POSTGRES_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Database.Postgres.Port = port
		}
	}
	if v := os.Getenv("POSTGRES_USER"); v != "" {
		cfg.Database.Postgres.User = v
	}
	if v := os.Getenv("POSTGRES_PASSWORD"); v != "" {
		cfg.Database.Postgres.Password = v
	}
	if v := os.Getenv("POSTGRES_DB"); v != "" {
		cfg.Database.Postgres.DBName = v
	}
	if v := os.Getenv("KAFKA_BROKERS"); v != "" {
		cfg.Kafka.Brokers = strings.Split(v, ",")
	}
	if v := os.Getenv("ETCD_ENDPOINTS"); v != "" {
		cfg.Etcd.Endpoints = strings.Split(v, ",")
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		parts := strings.Split(v, ":")
		if len(parts) == 2 {
			cfg.Redis.Host = parts[0]
			if port, err := strconv.Atoi(parts[1]); err == nil {
				cfg.Redis.Port = port
			}
		}
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		cfg.Redis.Password = v
	}
	if v := os.Getenv("MILVUS_ADDRESS"); v != "" {
		parts := strings.Split(v, ":")
		if len(parts) == 2 {
			cfg.Milvus.Host = parts[0]
			if port, err := strconv.Atoi(parts[1]); err == nil {
				cfg.Milvus.Port = port
			}
		}
	}
	if v := os.Getenv("ES_ADDRESSES"); v != "" {
		cfg.Elasticsearch.URL = v
	}
	if v := os.Getenv("NEO4J_URI"); v != "" {
		cfg.Neo4j.URI = v
	}
	if v := os.Getenv("NEO4J_USERNAME"); v != "" {
		cfg.Neo4j.User = v
	}
	if v := os.Getenv("NEO4J_PASSWORD"); v != "" {
		cfg.Neo4j.Password = v
	}
	if v := os.Getenv("MINIO_ENDPOINT"); v != "" {
		cfg.Minio.Endpoint = v
	}
	if v := os.Getenv("MINIO_ACCESS_KEY"); v != "" {
		cfg.Minio.AccessKey = v
	}
	if v := os.Getenv("MINIO_SECRET_KEY"); v != "" {
		cfg.Minio.SecretKey = v
	}
	if v := os.Getenv("ARK_API_KEY"); v != "" {
		cfg.Eino.APIKey = v
	}
	if v := os.Getenv("ARK_MODEL"); v != "" {
		cfg.Eino.Model = v
	}
	if v := os.Getenv("ARK_BASE_URL"); v != "" {
		cfg.Eino.BaseURL = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("TRACING_ENABLE"); v != "" {
		cfg.Tracing.Enable = v == "true" || v == "1"
	}
	if v := os.Getenv("OTEL_ENDPOINT"); v != "" {
		cfg.Tracing.OtelEndpoint = v
	}
	if v := os.Getenv("TRACING_SAMPLE_RATE"); v != "" {
		if rate, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Tracing.SampleRate = rate
		}
	}
	if v := os.Getenv("QQBRIDGE_ENABLED"); v != "" {
		cfg.QQBridge.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("QQBRIDGE_WS_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.QQBridge.WSPort = port
		}
	}
	if v := os.Getenv("QQBRIDGE_WS_FORWARD_URL"); v != "" {
		cfg.QQBridge.WSForwardURL = v
	}
}

// GetPostgresDSN 获取PostgreSQL连接字符串
func (c *Config) GetPostgresDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Postgres.Host,
		c.Database.Postgres.Port,
		c.Database.Postgres.User,
		c.Database.Postgres.Password,
		c.Database.Postgres.DBName,
		c.Database.Postgres.SSLMode,
	)
}

// GetRedisAddr 获取Redis地址
func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", c.Redis.Host, c.Redis.Port)
}

// GetKafkaBrokers 获取Kafka brokers
func (c *Config) GetKafkaBrokers() []string {
	return c.Kafka.Brokers
}

// GetServiceTimeout 获取服务超时时间
func (c *Config) GetServiceTimeout(service string) (time.Duration, error) {
	var timeoutStr string

	switch service {
	case "user":
		timeoutStr = c.Services.User.Timeout
	case "knowledge":
		timeoutStr = c.Services.Knowledge.Timeout
	case "question":
		timeoutStr = c.Services.Question.Timeout
	case "recommend":
		timeoutStr = c.Services.Recommend.Timeout
	case "collection":
		timeoutStr = c.Services.Collection.Timeout
	case "vector":
		timeoutStr = c.Services.Vector.Timeout
	case "search":
		timeoutStr = c.Services.Search.Timeout
	case "extraction":
		timeoutStr = c.Services.Extraction.Timeout
	case "message":
		timeoutStr = c.Services.Message.Timeout
	case "monitoring":
		timeoutStr = c.Services.Monitoring.Timeout
	case "im":
		timeoutStr = c.Services.IM.Timeout
	case "chat":
		timeoutStr = c.Services.Chat.Timeout
	case "contact":
		timeoutStr = c.Services.Contact.Timeout
	default:
		return 0, fmt.Errorf("unknown service: %s", service)
	}

	return time.ParseDuration(timeoutStr)
}

// GetEtcdDialTimeout 获取etcd拨号超时时间
func (c *Config) GetEtcdDialTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Etcd.DialTimeout)
}

// GetGRPCClientTimeout 获取gRPC客户端超时时间
func (c *Config) GetGRPCClientTimeout() (time.Duration, error) {
	return time.ParseDuration(c.GRPC.Client.Timeout)
}

// GetGRPCClientConnTimeout 获取gRPC客户端连接超时时间
func (c *Config) GetGRPCClientConnTimeout() (time.Duration, error) {
	return time.ParseDuration(c.GRPC.Client.ConnTimeout)
}

// GetGRPCServerTimeout 获取gRPC服务器超时时间
func (c *Config) GetGRPCServerTimeout() (time.Duration, error) {
	return time.ParseDuration(c.GRPC.Server.Timeout)
}

// GetUserServerAddr 获取用户服务地址
func (c *Config) GetUserServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.User)
}

// GetKnowledgeServerAddr 获取知识服务地址
func (c *Config) GetKnowledgeServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Knowledge)
}

// GetSearchServerAddr 获取搜索服务地址
func (c *Config) GetSearchServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Search)
}

// GetVectorServerAddr 获取向量服务地址
func (c *Config) GetVectorServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Vector)
}

// GetGatewayAddr 获取网关地址
func (c *Config) GetGatewayAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Gateway)
}

// GetIMServerAddr 获取IM服务地址
func (c *Config) GetIMServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.IM)
}

// GetChatServerAddr 获取聊天服务地址
func (c *Config) GetChatServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Chat)
}

// GetContactServerAddr 获取联系人服务地址
func (c *Config) GetContactServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Contact)
}

// GetBotServerAddr 获取Bot服务地址
func (c *Config) GetBotServerAddr() string {
	return fmt.Sprintf("0.0.0.0:%d", c.Ports.Bot)
}

// GetConfig 获取应用配置（单例模式）
func GetConfig() *Config {
	cfgOnce.Do(func() {
		var err error
		cfg, err = LoadConfig("./config")
		if err != nil {
			panic(fmt.Sprintf("Failed to load config: %v", err))
		}
	})
	return cfg
}
