# local-rag hybrid graph retrieval v1 fixtures

This directory vendors the provider fixtures from
`local-rag/tests/contract/fixtures/hybrid-graph-retrieval-v1` at commit
`cfbf6106a38bc46730217b987ca54c6b1b302f20`. The five provider JSON payloads
and their `manifest.json` are byte-for-byte consumer-test inputs; Eco Guardian
does not regenerate or modify them.

The matching `local-rag/api/openapi.yaml` SHA-256 is
`fd39c71846e49f0f6a7b4b1dc69a089634006af002d36af58c61444611df9369`.
`consumer-contract.json` records the subset Eco Guardian consumes: the exact
endpoint and snapshot binding, explicit-relationship default, bounds, RRF and
graph scoring, degradation modes, generation identities, evidence envelope,
retention boundary, and stable error behavior.

