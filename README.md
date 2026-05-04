# MonSys

[![Repo](https://img.shields.io/badge/github-MalteKiefer%2FMonSys-blue?logo=github)](https://github.com/MalteKiefer/MonSys)

Self-hosted server-monitoring stack: a Go control-plane server, a Go Linux
agent, a React single-page app, and TimescaleDB for metric storage. Designed
for small to mid-sized fleets where you want full ownership of the data
plane without running a hosted SaaS.

The Go module is `github.com/MalteKiefer/MonSys`; the shipped binaries are
named `mon-server` and `mon-agent` because the agent installs itself as
`/usr/local/bin/mon-agent`.

## Quick start

```sh
make web && docker compose -f deploy/docker-compose.yaml up -d
```

This builds the SPA into the server binary's embedded assets, then starts
the server, database, and reverse proxy defined in the compose file.

## Default admin

There is no shipped default admin account. After the stack is up:

1. The database password is read from `deploy/secrets/db_pw` (generate one
   before first boot).
2. Create the first admin user with:

   ```sh
   mon-server --create-user
   ```

   Follow the interactive prompts.

## Reporting vulnerabilities

See [SECURITY.md](./SECURITY.md) for the disclosure policy, scope, and SLA
targets. Please do not file public issues for security-relevant findings.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

TBD.
