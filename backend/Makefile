.PHONY: test-db-up migrate-test test

# Starts the shared docker-compose Postgres (the same container the app
# uses in development) and ensures the dedicated rrhh_go_test database
# exists. Test isolation from application data comes from the separate
# database, not a separate container.
test-db-up:
	docker compose up -d postgres
	@echo "waiting for postgres to be healthy..."
	@until docker compose exec -T postgres pg_isready -U rrhh_go -d rrhh_go >/dev/null 2>&1; do sleep 1; done
	docker compose exec -T postgres psql -U rrhh_go -d rrhh_go -tc \
		"SELECT 1 FROM pg_database WHERE datname = 'rrhh_go_test'" | grep -q 1 || \
		docker compose exec -T postgres createdb -U rrhh_go rrhh_go_test

# Migrations are applied by the testutil harness itself (internal/testutil),
# not by this target — this target exists for a developer who wants to
# inspect the test database's schema directly with `make migrate-test psql`.
migrate-test:
	docker compose exec -T postgres psql -U rrhh_go -d rrhh_go_test -f /docker-entrypoint-initdb.d/0001_init.sql
	docker compose exec -T postgres psql -U rrhh_go -d rrhh_go_test -f /docker-entrypoint-initdb.d/0002_auth.sql
	docker compose exec -T postgres psql -U rrhh_go -d rrhh_go_test -f /docker-entrypoint-initdb.d/0004_pay_zkteco.sql
	docker compose exec -T postgres psql -U rrhh_go -d rrhh_go_test -f /docker-entrypoint-initdb.d/0005_lookup_unique.sql

test:
	go test ./... -count=1
