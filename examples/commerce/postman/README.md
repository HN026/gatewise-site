# Commerce Postman checks

Start `python3 run.py --profile proxy-adaptive` from the extracted example. Import Commerce.postman_collection.json and the generated environment path printed by the launcher. Run all requests in order against a fresh launch, with request timeout 5s and script timeout 15s. The launcher supports direct, proxy-fixed, proxy-adaptive, middleware-fixed and middleware-adaptive. The expected_mode variable matches the chosen mode. Tests use only disposable local data and loopback control listeners. The full collection restores fault controls. To repeat cleanly, stop and relaunch the example. `python3 run.py --verify` runs the same portable collection with an installed Postman CLI.

For tenant-isolation checks, launch with `--tenants` and import Tenants.postman_collection.json instead. It requires the example cap 2 / tracking bound 32 / TTL 2s and synthetic loopback X-Demo-Tenant identities. The expiry request waits 2.3 seconds; keep script timeout at 15 seconds or more. The original Commerce collection runs with tenant isolation disabled.

Use `Priorities.postman_collection.json` with `run.py --priorities`. It checks server defaults, trusted overrides, downstream importance/cohorts, malformed metadata, compatibility mappings and bounded metric labels. Trust is loopback-only in this example; remote forgery and forwarded-IP spoofing are covered by Go integration tests.

`python3 validate_priority.py --output results/priority-validation.json` exercises trusted and untrusted orders entries during downstream SQL pressure. It preserves trust on the internal proxy-to-backend hop and checks that forged forwarding headers do not promote a caller.
