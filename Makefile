.PHONY: dev api web test

api:
	go run ./backend/cmd/api

test:
	go test -race ./backend/...

dev:
	docker compose up -d --build
