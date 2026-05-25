package governance

import "time"

type Config struct {
	Timeout        TimeoutConfig        `mapstructure:"timeout"`
	Retry          RetryConfig          `mapstructure:"retry"`
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
}

type TimeoutConfig struct {
	ServerDefault time.Duration `mapstructure:"server_default"`
	ClientDefault time.Duration `mapstructure:"client_default"`
	LLMDefault    time.Duration `mapstructure:"llm_default"`
}

type RetryConfig struct {
	MaxAttempts    int           `mapstructure:"max_attempts"`
	InitialDelay   time.Duration `mapstructure:"initial_delay"`
	MaxDelay       time.Duration `mapstructure:"max_delay"`
	RetryableCodes []string      `mapstructure:"retryable_codes"`
}

type CircuitBreakerConfig struct {
	MaxRequests      int32         `mapstructure:"max_requests"`
	Interval         time.Duration `mapstructure:"interval"`
	Timeout          time.Duration `mapstructure:"timeout"`
	FailureThreshold int32         `mapstructure:"failure_threshold"`
	SuccessThreshold int32         `mapstructure:"success_threshold"`
}

type RateLimitConfig struct {
	MaxRequestsPerSecond float64 `mapstructure:"max_requests_per_second"`
	MaxConcurrent        int     `mapstructure:"max_concurrent"`
}

func DefaultConfig() *Config {
	return &Config{
		Timeout: TimeoutConfig{
			ServerDefault: 30 * time.Second,
			ClientDefault: 10 * time.Second,
			LLMDefault:    120 * time.Second,
		},
		Retry: RetryConfig{
			MaxAttempts:    3,
			InitialDelay:   200 * time.Millisecond,
			MaxDelay:       5 * time.Second,
			RetryableCodes: []string{"UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED"},
		},
		CircuitBreaker: CircuitBreakerConfig{
			MaxRequests:      3,
			Interval:         30 * time.Second,
			Timeout:          10 * time.Second,
			FailureThreshold: 50,
			SuccessThreshold: 2,
		},
		RateLimit: RateLimitConfig{
			MaxRequestsPerSecond: 1000,
			MaxConcurrent:        200,
		},
	}
}

func (c *Config) IsRetryableCode(code string) bool {
	for _, rc := range c.Retry.RetryableCodes {
		if rc == code {
			return true
		}
	}
	return false
}
