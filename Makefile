.PHONY: demo demo-down demo-reset demo-smoke

demo:
	docker compose up --build --detach --wait

demo-down:
	docker compose down

demo-reset:
	docker compose down --volumes

demo-smoke: demo
	./scripts/demo-smoke.sh
