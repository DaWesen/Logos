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
	Log           Log           `mapstructure:"log"`
	JWT           JWT           `mapstructure:"jwt"`
	Prometheus    Prometheus    `mapstructure:"prometheus"`
	Tracing       Tracing       `mapstructure:"tracing"`
	Etcd          Etcd          `mapstructure:"etcd"`
	Kitex         Kitex         `mapstructure:"kitex"`
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
	Message    int `mapstructure:"message"`
	Monitoring int `mapstructure:"monitoring"`
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
	Message    ServiceConfig `mapstructure:"message"`
	Monitoring ServiceConfig `mapstructure:"monitoring"`
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

// Prometheus 配置
type Prometheus struct {
	Enable         bool   `mapstructure:"enable"`
	Port           int    `mapstructure:"port"`
	Path           string `mapstructure:"path"`
	UserPort       int    `mapstructure:"user_port"`
	KnowledgePort  int    `mapstructure:"knowledge_port"`
	QuestionPort   int    `mapstructure:"question_port"`
	RecommendPort  int    `mapstructure:"recommend_port"`
	CollectionPort int    `mapstructure:"collection_port"`
	VectorPort     int    `mapstructure:"vector_port"`
	SearchPort     int    `mapstructure:"search_port"`
	ExtractionPort int    `mapstructure:"extraction_port"`
	MessagePort    int    `mapstructure:"message_port"`
	MonitoringPort int    `mapstructure:"monitoring_port"`
	GatewayPort    int    `mapstructure:"gateway_port"`
}

// Tracing 追踪配置
type Tracing struct {
	Enable         bool    `mapstructure:"enable"`
	JaegerEndpoint string  `mapstructure:"jaeger_endpoint"`
	SampleRate     float64 `mapstructure:"sample_rate"`
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

// Kitex 配置
type Kitex struct {
	Client KitexClient `mapstructure:"client"`
	Server KitexServer `mapstructure:"server"`
}

// KitexClient Kitex客户端配置
type KitexClient struct {
	Timeout             string `mapstructure:"timeout"`
	ConnTimeout         string `mapstructure:"conn_timeout"`
	RetryTimes          int    `mapstructure:"retry_times"`
	MaxIdleConns        int    `mapstructure:"max_idle_conns"`
	MaxIdleConnsPerHost int    `mapstructure:"max_idle_conns_per_host"`
}

// KitexServer Kitex服务器配置
type KitexServer struct {
	Timeout            string         `mapstructure:"timeout"`
	MaxConns           int            `mapstructure:"max_conns"`
	MaxPendingRequests int            `mapstructure:"max_pending_requests"`
	IdleTimeout        string         `mapstructure:"idle_timeout"`
	Keepalive          KitexKeepalive `mapstructure:"keepalive"`
}

// KitexKeepalive Kitex心跳配置
type KitexKeepalive struct {
	Enable   bool   `mapstructure:"enable"`
	Interval string `mapstructure:"interval"`
	Timeout  string `mapstructure:"timeout"`
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
	default:
		return 0, fmt.Errorf("unknown service: %s", service)
	}

	return time.ParseDuration(timeoutStr)
}

// GetEtcdDialTimeout 获取etcd拨号超时时间
func (c *Config) GetEtcdDialTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Etcd.DialTimeout)
}

// GetKitexClientTimeout 获取Kitex客户端超时时间
func (c *Config) GetKitexClientTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Client.Timeout)
}

// GetKitexClientConnTimeout 获取Kitex客户端连接超时时间
func (c *Config) GetKitexClientConnTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Client.ConnTimeout)
}

// GetKitexServerTimeout 获取Kitex服务器超时时间
func (c *Config) GetKitexServerTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Server.Timeout)
}

// GetKitexServerIdleTimeout 获取Kitex服务器空闲超时时间
func (c *Config) GetKitexServerIdleTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Server.IdleTimeout)
}

// GetKitexKeepaliveInterval 获取Kitex心跳间隔
func (c *Config) GetKitexKeepaliveInterval() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Server.Keepalive.Interval)
}

// GetKitexKeepaliveTimeout 获取Kitex心跳超时时间
func (c *Config) GetKitexKeepaliveTimeout() (time.Duration, error) {
	return time.ParseDuration(c.Kitex.Server.Keepalive.Timeout)
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
