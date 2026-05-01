package config

import (
	"fmt"
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

// LoadConfig 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	err = viper.Unmarshal(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
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
