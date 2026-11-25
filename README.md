# mem0 Pack

A Nebari software pack that runs [mem0](https://github.com/mem0ai/mem0), an open-source memory
layer for AI agents. It gives each person a memory that persists across agents, sessions and
models: preferences they've stated, and context about the work an agent is doing for them.

Declared maturity level: **Experimental** (`level: experimental` in `pack-metadata.yaml`). It
does not install yet.

## Who it's for

Platform teams running AI agents on Nebari who want agents to remember things about the people
they work for, without that memory living with a model vendor. The first consumer is
[Tapas](https://github.com/nebari-dev/tapas), which calls mem0 on behalf of its agents and
enforces who can read which memories.

## What it will deploy

- The mem0 server, with an admin API key held in a Kubernetes Secret
- Postgres with pgvector for the memories
- A NetworkPolicy that admits only the services you name (for Tapas, its gateway pods)

mem0 has no per-user access control of its own: anything holding the API key can read any
person's memories. The pack keeps the key and the network path closed, and the calling service
is responsible for scoping each request to the right person.

## Deploy

Not available yet. The chart and the deploy command will land here.

## Known limitations

- Not installable yet.
