module github.com/arkeep-io/arkeep/server

go 1.27.0

toolchain go1.27.4

require (
	github.com/arkeep-io/arkeep/shared v0.0.0
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-chi/chi/v5 v5.5.0
	github.com/go-co-op/gocron/v2 v2.23.1
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang-migrate/migrate/v4 v4.20.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/pquerna/otp v1.5.0
	github.com/prometheus/client_golang v1.25.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/cobra v1.10.2
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.59.0
	golang.org/x/oauth2 v0.38.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
	gorm.io/driver/postgres v1.6.3
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.2
	modernc.org/sqlite v1.62.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boombuler/barcode v1.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/docker/go-connections v0.8.2 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.9 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.13.0 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lib/pq v1.12.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-sqlite3 v1.14.53 // indirect
	github.com/morikuni/aec v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pandatix/go-abnf v0.5.5 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.74.0 // indirect
	github.com/prometheus/procfs v0.23.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.1.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261110000943-09e21b237f2c // indirect
	modernc.org/libc v1.81.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/arkeep-io/arkeep/shared => ../shared
