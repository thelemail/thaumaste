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
	Signing  Signing
	Logger   Logger
}

type Server struct {
	// InstanceName is recorded on every event this process writes, so a second writer can be
	// added later without a migration.
	InstanceName    string        `env:"THAUMASTE_SERVER_INSTANCE_NAME"     envDefault:"default"`
	Addr            string        `env:"THAUMASTE_SERVER_ADDR"             envDefault:":8008"`
	ReadTimeout     time.Duration `env:"THAUMASTE_SERVER_READ_TIMEOUT"     envDefault:"15s"`
	WriteTimeout    time.Duration `env:"THAUMASTE_SERVER_WRITE_TIMEOUT"    envDefault:"30s"`
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

type Signing struct {
	MasterKey     string        `env:"THAUMASTE_SIGNING_MASTER_KEY,required,unset"`
	NextMasterKey string        `env:"THAUMASTE_SIGNING_NEXT_MASTER_KEY,unset"`
	KeyValidity   time.Duration `env:"THAUMASTE_SIGNING_KEY_VALIDITY"           envDefault:"24h"`
}

type Logger struct {
	Level  string `env:"THAUMASTE_LOG_LEVEL"  envDefault:"info"`
	Format string `env:"THAUMASTE_LOG_FORMAT" envDefault:"json"`
}

func Load() (Config, error) {
	_ = godotenv.Load(".env")
	return env.ParseAs[Config]()
}
