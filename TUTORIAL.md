# Tutorial: publish "Countries I have visited" and ship updates

You will put an App on Qwibi that shows every country's outline and lets each person mark the
countries they have visited. Then you will ship two updates to everyone who uses it, with no step
by anyone else.

You need Go, this repository and a Qwibi account. You need no Qwibi source code, no server and no
App token. Everything here uses public things only: the Qwibi web app, the public Go SDK
(`github.com/qwibi/qwibi-go-sdk`) and the development stand at
<https://qwibi.local.qwibi.com>.

## 0. What you are building

A Qwibi App either runs its own process or runs none. This one runs none. It consists of:

- a **release**: the declaration of one version. It says what a `country` is (its properties and
  its card), what it is called in each language and, from 1.2.0, what a person can mark on it
  (`visited`);
- **App data**: the 177 country outlines, stored by Qwibi.

Qwibi draws the outlines on the map of each person who adds the App, keeps each person's marks,
and moves everyone to your next version when you publish it. Your only job is to publish.

The repository declares three versions in `declaration.go`:

| Version | Adds |
| --- | --- |
| 1.0.0 | the `country` type, its card, English labels, the 177 outlines |
| 1.1.0 | an optional `subregion` property with a "Region" row on the card; Russian labels |
| 1.2.0 | the `visited` mark: each person can mark the countries they have been to |

## 1. Get the code

Install [Go](https://go.dev/dl/) 1.26 or later, then:

```sh
git clone https://github.com/qwibi/qwibi-countries
cd qwibi-countries
go test ./...
```

The first build downloads `github.com/qwibi/qwibi-go-sdk` and `github.com/qwibi/qwibi-proto-go`
from the public Go module proxy. The tests run offline. They check the outlines, every declared
version, and the whole publish flow against an in-process stand-in for Qwibi.

### Make it yours

Edit `publisher.go`: the publisher name people will see, and your support, privacy and licence
links (HTTPS only). These values are part of every release's content, so:

- in `countries_test.go`, empty the `publishedContent` map. It pins the content of the versions
  this repository has published, and yours will differ. Add each version back once you have
  published it, so that a later edit cannot change a published version by accident;
- if you rename the module in `go.mod`, change the import in `cmd/qwibi-countries/main.go` too.

## 2. Create an organization, the App and a key

Open <https://qwibi.local.qwibi.com>, sign in with your email, and go to **Settings →
Developer**.

1. **Organization.** Choose **New organization**, enter an **Organization name** such as
   "Travel maps", and **Create organization**. The organization owns your Apps and your keys.
2. **App.** Under Apps choose **New App** and fill in:
   - **App name**: *Countries I have visited*;
   - **App handle**: for example `my-countries`. The handle is your App's permanent address:
     lower-case letters, digits and hyphens, globally unique;
   - **Who can use it**: **Public**, so people find the App in the catalogue and add it
     themselves. **By link** and **Private** work too; the command finds such an App with your
     key.

   Then **Create App**. Settings show the App's id, a UUID. The command accepts either the handle
   or the id.
3. **Organization key.** Under Organization keys choose **New key**, give it a **Key name** such
   as "publish from laptop", and **Create key**. Copy the key now: it is shown once. The key lets
   scripts create Apps, publish releases and manage App tokens for this organization.

You do **not** need an App token. An App token is how a running App process acts as the App, and
this App has no process.

Keep the key out of the repository (`.env` is ignored by git):

```sh
export QWIBI_ORGANIZATION_KEY='…the key…'
```

If the key leaks, revoke it in settings and create another. Revoking it changes nothing for the
people using the App.

## 3. Check locally

```sh
go run ./cmd/qwibi-countries check
```

```text
1.0.0   177 countries  content 1d5af79874716c90
1.1.0   177 countries  content 407f9671c6474265
1.2.0   177 countries  content 748838e082a081c7  mark visited
ok: 3 versions, each compatible with the one before
```

(Your content hashes differ once you have edited `publisher.go`.)

`check` runs Qwibi's publication rules locally and sends nothing:

- the contract's field rules, on the exact requests the command will send;
- the release rules: contract range, fallbacks, HTTPS links, and that every card row names a
  declared property with an English label;
- the mark rules: a valid id, a declared object type, a colour, and a label in every language;
- every object against its version's schema;
- every version against the one before it (step 6).

## 4. Publish version 1.0.0

The command talks to `qwibi.local.qwibi.com:443` over TLS by default. It uses native gRPC:
requests with the `application/grpc` content type go straight to Qwibi's API, while the web app on
the same address keeps working as usual. To use another Qwibi, set `QWIBI_GRPC_ENDPOINT`
(`host:port`); add `--insecure` for one running on your own machine without TLS.

The repository declares three versions, so name the first one:

```sh
go run ./cmd/qwibi-countries publish --app my-countries --version 1.0.0
```

```text
published 1.0.0 as release …
wrote 177 countries; 1.0.0 is current for everyone who added the App
```

What happened:

1. The command looked up `my-countries`: without credentials first, because a Public App's
   handle is public, then with your key for a By-link or Private App.
2. It published the release through the SDK (`SealAndPublishAppRelease`). The SDK derived the
   release id from the App and the version and computed the content hash. Qwibi checked both,
   checked the declaration and made 1.0.0 the App's current version.
3. It replaced the App's data with the 177 countries (`ReplaceAppObjects`). The release goes
   first because it declares the `country` type the data is written under.

Run the same command again and nothing changes. The release id and publication time come from the
App and the version, never from the clock or a random source, so Qwibi recognises the second
attempt as the same publication and replays it. After any failure, just run the command again.

```sh
go run ./cmd/qwibi-countries status --app my-countries
```

```text
App …
  1.0.0   release …  published 2026-09-26  (current)
  177 countries stored
```

## 5. A person adds the App to My map

Now switch roles. You are someone who wants to track their travels.

1. Open Qwibi and find *Countries I have visited* in the catalogue, or open a link to it that the
   developer shared.
2. Add it to My map. My map needs a confirmed email; without one, Qwibi asks for it and sends a
   link, and following the link finishes the Add.
3. My map shows the outline of every country.

Tap a country to open its card. It shows the name, the continent, the country code and the ISO
code, in the language of your Qwibi, falling back to English.

## 6. Ship a compatible update: version 1.1.0

Back to the developer. Version 1.1.0 is already declared in `declaration.go`:

```go
{
	Semantic:    "1.1.0",
	PublishedAt: time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC),
	Subregion:   true, // an optional "subregion" property and a "Region" row on the card
	Russian:     true, // a Russian localization
},
```

Publish it:

```sh
go run ./cmd/qwibi-countries publish --app my-countries --version 1.1.0
go run ./cmd/qwibi-countries status  --app my-countries
```

Every person who added the App now has 1.1.0: the card shows the region, and Russian speakers see
Russian labels. Nobody updates or re-adds anything. Qwibi never runs two versions of one App side
by side; a new version replaces the old one for everyone at once.

That is why Qwibi refuses a version that would strand stored data or marks. 1.1.0 passes because:

- **nothing becomes required.** `subregion` is optional; the three required properties stay the
  same three;
- **nothing is removed or narrowed.** Every property keeps its type and constraints, and the
  `country` type stays declared;
- **the new key is new.** 1.0.0 declared `additionalProperties: false`, so no stored object could
  hold a `subregion`. Had 1.0.0 left the schema open, `subregion` could already hold anything, and
  declaring it a string would narrow stored data (see `TestOpenSchemaCannotGainAProperty`);
- **it comes later.** Its publication time is after 1.0.0's.

`check` runs these rules locally against the previous version's data. The tests in
`compat_test.go` show what each rule refuses: a newly required property, a changed type, a
tightened constraint, a removed property that stored objects carry, a new key that stored objects
already carry, and a publication time that goes back.

## 7. Add marks: version 1.2.0

Version 1.2.0 adds one thing to the release, the `visited` mark from `marks.go`:

```go
var VisitedMark = &pb.AppMarkDefinition{
	MarkId:               "visited",   // stable forever: a person's marks are keyed by it
	ObjectType:           ObjectType,  // marks apply to countries
	LabelLocalizationKey: "mark.visited",
	Single:               false,       // a person may mark many countries
	MarkedStyle:          "#16A34A",   // the accent of a marked country
	ShowCount:            true,        // show how many countries the person marked
}
```

The App stores nothing for it and never sees anyone's marks. Publish it:

```sh
go run ./cmd/qwibi-countries publish --app my-countries
```

Without `--version` the command publishes the latest declared version, 1.2.0. Adding a mark is a
compatible change: no stored object changes.

## 8. Mark the countries you have visited

Switch roles again.

1. Open a country's card and turn on **Visited**. The country takes the mark's accent on your map.
2. Qwibi counts your marked countries.
3. Your marks are yours alone. Only you see them, and they are erased with your account. The
   developer never sees them.
4. Without a confirmed email, marks stay on this device. When you confirm an email, Qwibi imports
   them into your account once.

## Your own next version

1. Append a `Version` to `Versions` with a higher SemVer and a later `PublishedAt`. Never edit a
   published one: Qwibi refuses a published version with different content.
2. Put the change behind a new field of `Version`, as `Subregion` and `Marks` do. Every older
   version must keep building exactly what it published; `publishedContent` in
   `countries_test.go` catches it if one does not.
3. Run `go test ./...` and `check`, then `publish`.

If the change cannot be compatible (removing a type people have marked, making a property
required), it is not an update of this App. Publish it as a new App.

## When something goes wrong

| Message | Meaning |
| --- | --- |
| `an organization key is required` | `QWIBI_ORGANIZATION_KEY` is not set (step 2). |
| `no App with handle … is visible to this organization key` | Wrong handle, or the key belongs to another organization. Pass the App id instead to tell the two apart. |
| `the organization key was not accepted` | The key is revoked, expired or mistyped. |
| `the key may not publish this App` | The key belongs to another organization. |
| `already published with different content` | You changed a published version. Declare a new one. |
| `refused … as incompatible` | The new version breaks a rule of step 6; `check` names it. |
| `data left as is: 1.2.0 is current` | You re-published an older version. Its release replayed; its data must not replace the current version's. |
| `does not pass the local check` | `check` shows which rule the version breaks; nothing was sent. |
