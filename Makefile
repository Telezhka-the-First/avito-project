APP_NAME        ?= review-assigner
CMD_DIR         ?= ./cmd/server
BIN_DIR         ?= ./bin
PKG             ?= ./...
DOCKER_COMPOSE  ?= docker compose

# DSN для интеграционных тестов (совпадает с тем, что используется в коде/compose)
TEST_DB_DSN     ?= postgres://reassv:reassv@localhost:5432/reasdb?sslmode=disable

# k6
K6                  ?= k6
K6_LOAD_SCRIPT      ?= ./load_test.js
K6_DEACT_SCRIPT     ?= ./load_deactivate.js

.PHONY: help build run test test-short test-integration lint tidy \
        docker-build docker-run up down logs db-up db-down \
        load-test load-deactivate load

help:
	@echo "Доступные команды:"
	@echo "  make build              - собрать бинарник приложения"
	@echo "  make run                - запустить приложение локально"
	@echo "  make test               - поднять БД через docker-compose и прогнать тесты"
	@echo "  make test-short         - прогнать тесты без поднятия БД (ожидает работающую БД)"
	@echo "  make test-integration   - alias для test"
	@echo "  make lint               - запустить golangci-lint (если установлен)"
	@echo "  make tidy               - go mod tidy"
	@echo "  make docker-build       - собрать Docker-образ приложения"
	@echo "  make docker-run         - запустить контейнер с приложением"
	@echo "  make up                 - docker-compose up -d (app + db)"
	@echo "  make down               - docker-compose down"
	@echo "  make db-up              - поднять только БД"
	@echo "  make db-down            - остановить только БД"
	@echo "  make load-test          - запустить k6 load_test.js (нагрузочный сценарий)"
	@echo "  make load-deactivate    - запустить k6 load_deactivate.js (нагрузка деактивации)"
	@echo "  make load               - последовательно запустить оба k6-сценария"

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) $(CMD_DIR)

run:
	go run $(CMD_DIR)

test: db-up
	TEST_DATABASE_URL="$(TEST_DB_DSN)" go test $(PKG)

test-short:
	go test $(PKG)

test-integration: test

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

docker-build:
	docker build -t $(APP_NAME):latest .

docker-run:
	docker run --rm -p 8080:8080 \
		-e DATABASE_URL="postgres://reassv:reassv@host.docker.internal:5432/reasdb?sslmode=disable" \
		$(APP_NAME):latest

up:
	$(DOCKER_COMPOSE) up -d

down:
	$(DOCKER_COMPOSE) down

logs:
	$(DOCKER_COMPOSE) logs -f app

db-up:
	$(DOCKER_COMPOSE) up -d db

db-down:
	$(DOCKER_COMPOSE) stop db

load-test: up
	$(K6) run $(K6_LOAD_SCRIPT)

load-deactivate: up
	$(K6) run $(K6_DEACT_SCRIPT)

load: load-test load-deactivate
