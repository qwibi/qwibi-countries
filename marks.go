package countries

import (
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/proto"
)

// VisitedMark is the one mark of this App, declared from version 1.2.0 on.
//
// A mark belongs to the person who sets it, not to the App: Qwibi stores it
// per person and App, shows it only to that person, and erases it with their
// account. The App never sees it and stores nothing for it. A person without
// a confirmed email keeps their marks on the device; when they confirm one,
// Qwibi imports those marks into their account once.
var VisitedMark = &pb.AppMarkDefinition{
	// MarkId is stable forever: a person's marks are keyed by it.
	MarkId: "visited",
	// ObjectType is the kind of object the mark applies to.
	ObjectType: ObjectType,
	// LabelLocalizationKey names the toggle on the country's card.
	LabelLocalizationKey: "mark.visited",
	// Single would allow at most one marked country per person. A traveller
	// has been to many, so it stays false.
	Single: false,
	// MarkedStyle is the accent of a country the person has marked: a
	// literal colour, or a semantic colour token of the host.
	MarkedStyle: "#16A34A",
	// ShowCount asks the host to show how many countries the person marked.
	ShowCount: true,
}

// visitedCountLabelKey was declared by the first versions for a count label.
// The host labels the count with the mark's own label, so nothing reads the
// key; it stays in every version so that 1.0.0 and 1.1.0 keep building
// exactly the bytes they were published with.
const visitedCountLabelKey = "mark.visited.count"

// marks is the mark list of one version's release.
func marks(v Version) []*pb.AppMarkDefinition {
	if !v.Marks {
		return nil
	}
	return []*pb.AppMarkDefinition{proto.Clone(VisitedMark).(*pb.AppMarkDefinition)}
}
