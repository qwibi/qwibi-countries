# Countries I have visited — a Qwibi App

A person adds this App to their map, sees the outline of every country, and marks the ones they
have been to. The marked countries turn green on their map, and Qwibi counts them.

It is also the worked example of a Qwibi App built outside the Qwibi codebase, using only the
public Go SDK. [TUTORIAL.md](TUTORIAL.md) walks the whole path: create the App in developer
settings, publish it, add it to My map as a person, ship compatible updates, and mark countries.

## How it works

The App runs **no process**. It is a release declaration plus App data, published once by a
command:

- the **release** declares the `country` object type (its schema, its card and its labels in
  English and, from 1.1.0, Russian, which Qwibi does not show yet) and, from 1.2.0, the `visited`
  mark;
- the **App data** is 177 country outlines, one `country` object each, written once per version.
  Each country is updated in place by its handle, so it keeps its object id across versions and
  people's marks survive every release;
- **Qwibi** does everything else. It draws the outlines on the map of every person who adds the
  App, keeps each person's marks, and moves every installation to a new version when one is
  published.

Because nothing runs, there is no App token, no server to host, and nothing to keep up.
Publishing needs only an organization key, created in the web app's developer settings.

## Repository

| Path | What it is |
| --- | --- |
| `declaration.go` | the versions and the release each one declares |
| `marks.go` | the `visited` mark, declared from 1.2.0 |
| `data.go`, `data/countries.geojson` | the country outlines and the objects built from them |
| `publisher.go` | the publisher name and links; edit them to make the App yours |
| `validate.go`, `compat.go` | the local check: Qwibi's publication rules, run before anything is sent |
| `publish.go` | publication and App data through the Go SDK |
| `cmd/qwibi-countries` | the command: `check`, `publish`, `status` |
| `tools/naturalearth` | regenerates `data/countries.geojson` from Natural Earth |

## Commands

```sh
go test ./...                                   # everything, offline
go run ./cmd/qwibi-countries check              # every version, and each against the one before
export QWIBI_ORGANIZATION_KEY=…                 # from developer settings
go run ./cmd/qwibi-countries publish --app <handle> --version 1.0.0
go run ./cmd/qwibi-countries publish --app <handle>                   # the latest version
go run ./cmd/qwibi-countries status  --app <handle>
```

The command speaks native gRPC to the development stand, `qwibi.local.qwibi.com:443` over TLS.
`QWIBI_GRPC_ENDPOINT` sets another address; add `--insecure` for a Qwibi running on your own
machine without TLS.

Publishing is safe to repeat. A version's release id and publication time are derived from the
App and the version, so publishing the same version again sends the same release, and Qwibi
answers with the release it already stored instead of refusing it or storing a second one. Qwibi's
answer does not say whether the release was new, so the command prints the same
`published … as release …` line both times. The data write replaces the whole data set, matching
countries by handle, so stored countries keep their object ids. After any failure, run the same
command again.

## Dependencies

Only public modules from proxy.golang.org: `github.com/qwibi/qwibi-go-sdk` v1.2.0 and
`github.com/qwibi/qwibi-proto-go` v1.2.0, plus `buf.build/go/protovalidate` to run the contract's
field rules locally. Go 1.26 or later.

## Data

Country outlines: [Natural Earth](https://www.naturalearthdata.com/) 1:110m Admin 0 – Countries,
public domain. They are trimmed by `tools/naturalearth`: five properties, coordinates rounded to
four decimals, sorted by code. Borders and names follow Natural Earth's de facto convention.
Among the 177 are Northern Cyprus, Somaliland and Kosovo.

## Licence

Apache-2.0, see [LICENSE](LICENSE).
