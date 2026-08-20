package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv string `env:"THAUMASTE_APP_ENV" envDefault:"local"`

	Server   Server
	Health   Health
	Postgres Postgres
	Logger   Logger
}

type Server struct {
	Addr            string        `env:"THAUMASTE_SERVER_ADDR"             envDefault:":8008"`
	ReadTimeout     time.Duration `env:"THAUMASTE_SERVER_READ_TIMEOUT"     envDefault:"15s"`
	WriteTimeout    time.Duration `env:"THAUMASTE_SERVER_WRITE_TIMEOUT"    envDefault:"30s"`
	ShutdownTimeout time.Duration `env:"THAUMASTE_SERVER_SHUTDOWN_TIMEOUT" envDefault:"30s"`
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
}

func (p Postgres) DSN() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.User, p.Password),
		Host:     fmt.Sprintf("%s:%d", p.Host, p.Port),
		Path:     p.Database,
		RawQuery: "sslmode=" + url.QueryEscape(p.SSLMode),
	}
	return u.String()
}

type Logger struct {
	Level  string `env:"THAUMASTE_LOG_LEVEL"  envDefault:"info"`
	Format string `env:"THAUMASTE_LOG_FORMAT" envDefault:"json"`
}

func Load() (Config, error) {
	_ = godotenv.Load(".env")
	return env.ParseAs[Config]()
}
