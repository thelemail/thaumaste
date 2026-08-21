package config

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv string `env:"THAUMASTE_APP_ENV" envDefault:"local"`

	Server   Server
	Health   Health
	Postgres Postgres
	Valkey   Valkey
	Signing  Signing
	Auth     Auth
	Limits   Limits
	Sync     Sync
	Keys     Keys
	Logger   Logger
}

type Server struct {
	InstanceName    string        `env:"THAUMASTE_SERVER_INSTANCE_NAME"     envDefault:"default"`
	Addr            string        `env:"THAUMASTE_SERVER_ADDR"             envDefault:":8008"`
	ReadTimeout     time.Duration `env:"THAUMASTE_SERVER_READ_TIMEOUT"     envDefault:"15s"`
	WriteTimeout    time.Duration `env:"THAUMASTE_SERVER_WRITE_TIMEOUT"    envDefault:"30s"`
	IdleTimeout     time.Duration `env:"THAUMASTE_SERVER_IDLE_TIMEOUT"     envDefault:"120s"`
	ShutdownTimeout time.Duration `env:"THAUMASTE_SERVER_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	PublicScheme    string        `env:"THAUMASTE_SERVER_PUBLIC_SCHEME"    envDefault:"https"`
}

type Health struct {
	Addr       string        `env:"THAUMASTE_HEALTH_ADDR"        envDefault:":8009"`
	DrainDelay time.Duration `env:"THAUMASTE_HEALTH_DRAIN_DELAY" envDefault:"5s"`
}

type Postgres struct {
	Host            string        `env:"THAUMASTE_POSTGRES_HOST"              envDefault:"127.0.0.1"`
	Port            int           `env:"THAUMASTE_POSTGRES_PORT"              envDefault:"5435"`
	User            string        `env:"THAUMASTE_POSTGRES_USER,required"`
	Password        string        `env:"THAUMASTE_POSTGRES_PASSWORD,required,unset"`
	Database        string        `env:"THAUMASTE_POSTGRES_DB,required"`
	SSLMode         string        `env:"THAUMASTE_POSTGRES_SSLMODE"           envDefault:"disable"`
	MaxOpenConns    int           `env:"THAUMASTE_POSTGRES_MAX_OPEN_CONNS"    envDefault:"25"`
	MaxIdleConns    int           `env:"THAUMASTE_POSTGRES_MAX_IDLE_CONNS"    envDefault:"5"`
	ConnMaxLifetime time.Duration `env:"THAUMASTE_POSTGRES_CONN_MAX_LIFETIME" envDefault:"30m"`

	StatementTimeout         time.Duration `env:"THAUMASTE_POSTGRES_STATEMENT_TIMEOUT"           envDefault:"15s"`
	LockTimeout              time.Duration `env:"THAUMASTE_POSTGRES_LOCK_TIMEOUT"                envDefault:"5s"`
	IdleInTransactionTimeout time.Duration `env:"THAUMASTE_POSTGRES_IDLE_IN_TRANSACTION_TIMEOUT" envDefault:"30s"`
	MigrateOnStart           bool          `env:"THAUMASTE_POSTGRES_MIGRATE_ON_START"            envDefault:"true"`
}

func (p Postgres) DSN() string {
	return p.dsn(map[string]time.Duration{
		"statement_timeout":                   p.StatementTimeout,
		"lock_timeout":                        p.LockTimeout,
		"idle_in_transaction_session_timeout": p.IdleInTransactionTimeout,
	})
}

func (p Postgres) MigratorDSN() string {
	return p.dsn(map[string]time.Duration{
		"lock_timeout": p.LockTimeout,
	})
}

func (p Postgres) dsn(timeouts map[string]time.Duration) string {
	q := url.Values{"sslmode": []string{p.SSLMode}}
	for name, d := range timeouts {
		if d > 0 {
			q.Set(name, strconv.FormatInt(d.Milliseconds(), 10))
		}
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.User, p.Password),
		Host:     fmt.Sprintf("%s:%d", p.Host, p.Port),
		Path:     p.Database,
		RawQuery: q.Encode(),
	}
	return u.String()
}

type Valkey struct {
	Addrs        []string      `env:"THAUMASTE_VALKEY_ADDRS"         envDefault:"127.0.0.1:6382"`
	Username     string        `env:"THAUMASTE_VALKEY_USERNAME"`
	Password     string        `env:"THAUMASTE_VALKEY_PASSWORD,unset"`
	SelectDB     int           `env:"THAUMASTE_VALKEY_DB"            envDefault:"0"`
	KeyPrefix    string        `env:"THAUMASTE_VALKEY_KEY_PREFIX"    envDefault:"thaumaste"`
	DialTimeout  time.Duration `env:"THAUMASTE_VALKEY_DIAL_TIMEOUT"  envDefault:"2s"`
	LockValidity time.Duration `env:"THAUMASTE_VALKEY_LOCK_VALIDITY" envDefault:"10s"`
}

type Limits struct {
	SendPerUser   int           `env:"THAUMASTE_LIMITS_SEND_PER_USER"   envDefault:"10"`
	SendPerRoom   int           `env:"THAUMASTE_LIMITS_SEND_PER_ROOM"   envDefault:"60"`
	SendPerTenant int           `env:"THAUMASTE_LIMITS_SEND_PER_TENANT" envDefault:"600"`
	SendWindow    time.Duration `env:"THAUMASTE_LIMITS_SEND_WINDOW"     envDefault:"5s"`
	TxnRetention  time.Duration `env:"THAUMASTE_LIMITS_TXN_RETENTION"   envDefault:"24h"`
	TxnSweepEvery time.Duration `env:"THAUMASTE_LIMITS_TXN_SWEEP_EVERY" envDefault:"1h"`
}

type Signing struct {
	MasterKey     string        `env:"THAUMASTE_SIGNING_MASTER_KEY,required,unset"`
	NextMasterKey string        `env:"THAUMASTE_SIGNING_NEXT_MASTER_KEY,unset"`
	KeyValidity   time.Duration `env:"THAUMASTE_SIGNING_KEY_VALIDITY"           envDefault:"24h"`
}

type Auth struct {
	AccessTokenTTL  time.Duration `env:"THAUMASTE_AUTH_ACCESS_TOKEN_TTL"  envDefault:"1h"`
	RefreshTokenTTL time.Duration `env:"THAUMASTE_AUTH_REFRESH_TOKEN_TTL" envDefault:"720h"`
	SessionTTL      time.Duration `env:"THAUMASTE_AUTH_SESSION_TTL"       envDefault:"15m"`

	Argon2Time    uint32 `env:"THAUMASTE_AUTH_ARGON2_TIME"    envDefault:"3"`
	Argon2MemoryK uint32 `env:"THAUMASTE_AUTH_ARGON2_MEMORY"  envDefault:"65536"`
	Argon2Threads uint8  `env:"THAUMASTE_AUTH_ARGON2_THREADS" envDefault:"4"`

	MaxFailures   int           `env:"THAUMASTE_AUTH_MAX_FAILURES"   envDefault:"10"`
	FailureWindow time.Duration `env:"THAUMASTE_AUTH_FAILURE_WINDOW" envDefault:"15m"`
	LockFor       time.Duration `env:"THAUMASTE_AUTH_LOCK_FOR"       envDefault:"15m"`

	AssertionKey string        `env:"THAUMASTE_AUTH_ASSERTION_KEY"`
	AssertionTTL time.Duration `env:"THAUMASTE_AUTH_ASSERTION_TTL" envDefault:"5m"`
}

type Sync struct {
	MaxTimeout      time.Duration `env:"THAUMASTE_SYNC_MAX_TIMEOUT"      envDefault:"25s"`
	ConnectionTTL   time.Duration `env:"THAUMASTE_SYNC_CONNECTION_TTL"   envDefault:"1h"`
	SweepEvery      time.Duration `env:"THAUMASTE_SYNC_SWEEP_EVERY"      envDefault:"10m"`
	MaxRoomsPerSync int           `env:"THAUMASTE_SYNC_MAX_ROOMS"        envDefault:"200"`
}

type Keys struct {
	MaxOneTimeKeys  int `env:"THAUMASTE_KEYS_MAX_ONE_TIME"     envDefault:"200"`
	MaxQueryUsers   int `env:"THAUMASTE_KEYS_MAX_QUERY_USERS"  envDefault:"200"`
	MaxClaimDevices int `env:"THAUMASTE_KEYS_MAX_CLAIM_DEVICES" envDefault:"200"`
}

type Logger struct {
	Level  string `env:"THAUMASTE_LOG_LEVEL"  envDefault:"info"`
	Format string `env:"THAUMASTE_LOG_FORMAT" envDefault:"json"`
}

func Load() (Config, error) {
	_ = godotenv.Load(".env")
	return env.ParseAs[Config]()
}
