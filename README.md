# MangoMan agent marketplace (registry)

This branch is the marketplace: `packages/` holds reviewed agent packages
(`.mmagent`, each signed by its creator) and `index.json` lists them, signed
with MangoMan's marketplace key. `mangoman agents search` and
`mangoman agents install NAME` read it and check every signature.

## Submit an agent

1. Build it: `mangoman agents new NAME`, edit, then `mangoman agents keygen` (once) and `mangoman agents pack NAME`.
2. Check it yourself: `mangoman agents review NAME-1.0.0.mmagent` must pass, and `mangoman agents eval NAME` should beat the free pack.
3. Open a pull request against this branch adding `packages/NAME-VERSION.mmagent`. The review runs on the pull request.
4. After a maintainer merges, the index is rebuilt and signed. Updates must be signed with the same creator key.

Agents run on the user's computer in MangoMan's sandbox, with only the network hosts and programs their agent.json declares.
