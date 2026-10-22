# Gatewise local demo

Requires Go and Python 3. Run `python3 run.py` from this directory. It builds the included Go modules, starts all seven local processes and prints their addresses. Ctrl+C stops only those processes. `--base-port 18080` avoids conflicts with existing local services. Set GO_BIN if Go is not on PATH.

Import postman/Gatewise.postman_collection.json and the generated postman/Gatewise-Demo.postman_environment.json. Run folders individually; do not run all stages without changing service state. See postman/README.md for routing, priorities, metrics and adaptive scenarios.

The stopped-backend stages require manual process control. The all-in-one launcher stops if any child exits; use separate terminals for those two failure stages. The launcher is intended for the normal, concurrency, priority, adaptive and metrics folders.

These are synthetic local demo services. They are not production infrastructure. The fixture control endpoint is loopback-only. No binaries, credentials, personal .env files, node_modules or historical results are included.
