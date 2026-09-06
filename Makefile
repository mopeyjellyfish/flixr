.PHONY: start stop logs test-offline demo demo-down demo-reset demo-smoke

start:
	mkdir -p media
	docker compose -f compose.local.yml up --detach --wait
	docker compose -f compose.local.yml logs --tail=20 flixr
	@echo "Open http://localhost:$${FLIXR_PORT:-8787} or use this server's LAN address."

stop:
	docker compose -f compose.local.yml down

logs:
	docker compose -f compose.local.yml logs --follow flixr

test-offline:
	./scripts/offline-smoke.sh

demo:
	python3 scripts/prepare-demo.py
	docker compose up --build --detach --wait

demo-down:
	docker compose down

demo-reset:
	docker compose down --volumes

demo-smoke:
	./scripts/demo-smoke.sh

# Vite serves the same showcase with React Fast Refresh; API and artwork stay local.
.PHONY: demo-dev
demo-dev:
	python3 scripts/prepare-demo.py
	FLIXR_DEMO_PORT=19880 docker compose up --build --detach --wait
	cd frontend && npm ci && FLIXR_API_TARGET=http://127.0.0.1:19880 npm run dev
