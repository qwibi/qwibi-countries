// Command qwibi-countries checks and publishes the "countries I have
// visited" Qwibi App.
//
//	qwibi-countries check
//	qwibi-countries publish --app <handle|app id> [--version 1.1.0]
//	qwibi-countries status  --app <handle|app id>
//
// publish and status read the organization key from
// QWIBI_ORGANIZATION_KEY and the endpoint from QWIBI_GRPC_ENDPOINT
// (default qwibi.local.qwibi.com:443, TLS). See TUTORIAL.md.
package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	countries "github.com/qwibi/qwibi-countries"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultEndpoint = "qwibi.local.qwibi.com:443"

// checkAppID stands in for a real App id in `check`: the content hash does
// not depend on it, and the id rules only need its shape.
const checkAppID = "00000000-0000-4000-8000-000000000000"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "check":
		err = runCheck(args[1:], stdout)
	case "publish":
		err = runPublish(ctx, args[1:], stdout)
	case "status":
		err = runStatus(ctx, args[1:], stdout)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "qwibi-countries:", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  qwibi-countries check                                  check every declared version locally
  qwibi-countries publish --app <handle|id> [--version]  publish a version (default: the latest)
  qwibi-countries status  --app <handle|id>              show published versions and stored countries

environment:
  QWIBI_ORGANIZATION_KEY  organization key from developer settings
  QWIBI_GRPC_ENDPOINT     Qwibi API host:port (default `+defaultEndpoint+`)
`)
}

func runCheck(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	app := flags.String("app", checkAppID, "App id, to print the release ids it will get")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := countries.Check(*app); err != nil {
		return err
	}
	for _, v := range countries.Versions {
		release, err := countries.SealedRelease(*app, v)
		if err != nil {
			return err
		}
		objects, err := countries.Objects(v)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%-7s %d countries  content %s", v.Semantic, len(objects), hex.EncodeToString(release.GetCanonicalContentSha256())[:16])
		for _, mark := range release.GetMarks() {
			fmt.Fprintf(stdout, "  mark %s", mark.GetMarkId())
		}
		if *app != checkAppID {
			fmt.Fprintf(stdout, "  release %s", release.GetReleaseId())
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "ok: %d versions, each compatible with the one before\n", len(countries.Versions))
	return nil
}

type connectFlags struct {
	app      *string
	insecure *bool
}

func addConnectFlags(flags *flag.FlagSet) connectFlags {
	return connectFlags{
		app:      flags.String("app", "", "the App's handle or id (developer settings show both)"),
		insecure: flags.Bool("insecure", false, "plaintext gRPC, for a Qwibi running on your own machine"),
	}
}

func connect(ctx context.Context, c connectFlags) (*countries.Publisher, string, error) {
	if *c.app == "" {
		return nil, "", fmt.Errorf("--app is required")
	}
	endpoint := os.Getenv("QWIBI_GRPC_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	creds := credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	if *c.insecure {
		creds = insecure.NewCredentials()
	}
	publisher, err := countries.NewPublisher(endpoint, os.Getenv("QWIBI_ORGANIZATION_KEY"), grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, "", fmt.Errorf("%w (set QWIBI_ORGANIZATION_KEY; see TUTORIAL.md)", err)
	}
	appID, err := publisher.ResolveApp(ctx, *c.app)
	if err != nil {
		publisher.Close()
		return nil, "", err
	}
	return publisher, appID, nil
}

func runPublish(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	conn := addConnectFlags(flags)
	version := flags.String("version", countries.Latest().Semantic, "version to publish")
	if err := flags.Parse(args); err != nil {
		return err
	}
	v, err := countries.FindVersion(*version)
	if err != nil {
		return err
	}
	publisher, appID, err := connect(ctx, conn)
	if err != nil {
		return err
	}
	defer publisher.Close()
	result, err := publisher.Publish(ctx, appID, v)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "published %s as release %s\n", v.Semantic, result.ReleaseID)
	if result.DataSkipped {
		fmt.Fprintf(stdout, "data left as is: %s is current, and an older version's data must not replace it\n", result.Current)
		return nil
	}
	fmt.Fprintf(stdout, "wrote %d countries; %s is current for everyone who added the App\n", result.Objects, result.Current)
	return nil
}

func runStatus(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	conn := addConnectFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	publisher, appID, err := connect(ctx, conn)
	if err != nil {
		return err
	}
	defer publisher.Close()
	releases, err := publisher.Releases(ctx, appID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "App %s\n", appID)
	for i, release := range releases {
		marker := ""
		if i == len(releases)-1 {
			marker = "  (current)"
		}
		fmt.Fprintf(stdout, "  %-7s release %s  published %s%s\n", release.GetSemanticVersion(), release.GetReleaseId(),
			release.GetPublishedAt().AsTime().Format("2006-01-02"), marker)
	}
	if len(releases) == 0 {
		fmt.Fprintln(stdout, "  no published version yet")
	}
	count, err := publisher.ObjectCount(ctx, appID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  %d countries stored\n", count)
	return nil
}
