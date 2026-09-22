module github.com/arkeep-io/arkeep/server

go 1.26.0

require (
	github.com/arkeep-io/arkeep/shared v0.0.0
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-chi/chi/v5 v5.3.2
	github.com/go-co-op/gocron/v2 v2.22.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang-migrate/migrate/v4 v4.20.1
	github.com/google/uuid v1.6.0
	github.com/pquerna/otp v1.5.0
	github.com/prometheus/client_golang v1.24.1
	github.com/spf13/cobra v1.10.2
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.57.0
	golang.org/x/oauth2 v0.37.0
	google.golang.org/grpc v1.83.2
	gorm.io/driver/postgres v1.6.3
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.2
	modernc.org/sqlite v1.59.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boombuler/barcode v1.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/docker/go-connections v0.8.1 // indirect
	github.com/morikuni/aec v1.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.71.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.46.0 // indirect
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.5 // indirect
	github.com/gorilla/websocket v1.5.3
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/lib/pq v1.12.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-sqlite3 v1.14.52 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/pflag v1.0.10 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260831171406-18b4a7587f8a // indirect
	google.golang.org/protobuf v1.36.12
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/arkeep-io/arkeep/shared => ../shared
