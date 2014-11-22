package libkb

import (
	"fmt"
	"github.com/keybase/go-jsonw"
	"sync"
	"time"
)

// Can either be a RemoteProofChainLink or one of the identities
// listed in a tracking statement
type TrackIdComponent interface {
	ToIdString() string
	ToKeyValuePair() (string, string)
}

type TrackSet map[string]TrackIdComponent

func (ts TrackSet) Add(t TrackIdComponent) {
	ts[t.ToIdString()] = t
}

func (a TrackSet) SubsetOf(b TrackSet) (missing []TrackIdComponent, ret bool) {
	ret = true
	for k, tc := range a {
		if _, found := b[k]; !found {
			ret = false
			missing = append(missing, tc)
		}
	}
	return
}

func (a TrackSet) MemberOf(t TrackIdComponent) bool {
	_, found := a[t.ToIdString()]
	return found
}

func (a TrackSet) Equal(b TrackSet) bool {
	if len(a) != len(b) {
		return false
	} else {
		_, ok := a.SubsetOf(b)
		return ok
	}
}

//=====================================================================

type TrackLookup struct {
	link  *TrackChainLink     // The original chain link that I signed
	set   TrackSet            // The total set of tracked identities
	ids   map[string][]string // A http -> [foo.com, boo.com] lookup
	mutex *sync.Mutex         // in case we're accessing in mutliple threads
}

func (tl *TrackLookup) ComputeKeyDiff(curr PgpFingerprint) TrackDiff {
	prev, err := tl.link.GetTrackedPgpFingerprint()
	if err != nil {
		return TrackDiffError{err}
	} else if prev.Eq(curr) {
		return TrackDiffNone{}
	} else {
		return TrackDiffClash{curr.ToQuads(), prev.ToQuads()}
	}
}

type TrackDiff interface {
	BreaksTracking() bool
	ToDisplayString() string
	IsSameAsTracked() bool
}

type TrackDiffError struct {
	err error
}

func (t TrackDiffError) BreaksTracking() bool {
	return true
}
func (t TrackDiffError) ToDisplayString() string {
	return ColorString("red", "<error>")
}
func (t TrackDiffError) IsSameAsTracked() bool {
	return false
}

type TrackDiffUpgraded struct {
	prev, curr string
}

func (t TrackDiffUpgraded) IsSameAsTracked() bool {
	return false
}

func (t TrackDiffUpgraded) BreaksTracking() bool {
	return false
}
func (t TrackDiffUpgraded) ToDisplayString() string {
	return ColorString("orange", "<Upgraded from "+t.prev+" to "+t.curr+">")
}

type TrackDiffNone struct{}

func (t TrackDiffNone) BreaksTracking() bool {
	return false
}
func (t TrackDiffNone) IsSameAsTracked() bool {
	return true
}

func (t TrackDiffNone) ToDisplayString() string {
	return ColorString("green", "<OK>")
}

type TrackDiffNew struct{}

func (t TrackDiffNew) BreaksTracking() bool {
	return false
}
func (t TrackDiffNew) IsSameAsTracked() bool {
	return false
}

type TrackDiffClash struct {
	observed, expected string
}

func (t TrackDiffNew) ToDisplayString() string {
	return ColorString("blue", "<new>")
}

func (t TrackDiffClash) BreaksTracking() bool {
	return true
}

func (t TrackDiffClash) ToDisplayString() string {
	return ColorString("red", "<CHANGED from "+t.expected+">")
}
func (t TrackDiffClash) IsSameAsTracked() bool {
	return false
}

type TrackDiffLost struct {
	idc TrackIdComponent
}

func (t TrackDiffLost) BreaksTracking() bool {
	return true
}
func (t TrackDiffLost) ToDisplayString() string {
	return ColorString("red", "Lost proof: "+t.idc.ToIdString())
}
func (t TrackDiffLost) IsSameAsTracked() bool {
	return false
}

func NewTrackLookup(link *TrackChainLink) *TrackLookup {
	sbs := link.ToServiceBlocks()
	set := make(TrackSet)
	ids := make(map[string][]string)
	for _, sb := range sbs {
		set.Add(sb)
		k, v := sb.ToKeyValuePair()
		list, found := ids[k]
		if !found {
			list = make([]string, 0, 1)
		}
		ids[k] = append(list, v)
	}
	ret := &TrackLookup{link: link, set: set, ids: ids, mutex: new(sync.Mutex)}
	return ret
}

func (l *TrackLookup) Lock() {
	l.mutex.Lock()
}

func (l *TrackLookup) Unlock() {
	l.mutex.Unlock()
}

func (e *TrackLookup) GetCTime() time.Time {
	return e.link.GetCTime()
}

//=====================================================================

type TrackEngine struct {
	TheirName    string
	Them         *User
	Me           *User
	Interactive  bool
	NoSelf       bool
	StrictProofs bool
	MeRequired   bool
}

func (e *TrackEngine) LoadThem() error {

	if e.Them == nil && len(e.TheirName) == 0 {
		return fmt.Errorf("No 'them' passed to TrackEngine")
	}
	if e.Them == nil {
		if u, err := LoadUser(LoadUserArg{
			Name:        e.TheirName,
			Self:        false,
			LoadSecrets: false,
			ForceReload: false,
		}); err != nil {
			return err
		} else {
			e.Them = u
		}
	}
	return nil
}

func (e *TrackEngine) LoadMe() error {
	if e.Me == nil {
		if me, err := LoadMe(LoadUserArg{LoadSecrets: true}); err != nil && e.MeRequired {
			return err
		} else {
			e.Me = me
		}
	}
	return nil
}

func (e *TrackEngine) Run() (err error) {

	var tmp []byte
	var jw *jsonw.Wrapper
	var key *PgpKeyBundle
	var sig string
	var sigid *SigId
	var warnings Warnings
	var un string

	if err = e.LoadThem(); err != nil {
		return
	} else if err = e.LoadMe(); err != nil {
		return
	} else if e.NoSelf && e.Me.Equal(*e.Them) {
		err = fmt.Errorf("Cannot track yourself")
	}

	un = e.Them.GetName()

	tracker := G.UI.GetIdentifyUI()

	res := e.Them.Identify(IdentifyArg{
		ReportHook: tracker.ReportHook,
		Me:         e.Me,
	})

	var prompt string
	if err, warnings = res.GetErrorAndWarnings(e.StrictProofs); err != nil {
		return
	} else if !warnings.IsEmpty() {
		tracker.ShowWarnings(warnings)
		prompt = "Some proofs failed; still track " + un + "?"
	} else if len(res.ProofChecks) == 0 {
		prompt = "We found an account for " + un +
			", but they haven't proven their identity. Still track them?"
	} else {
		prompt = "Is this the " + un + "you wanted?"
	}

	if err = tracker.PromptForConfirmation(prompt); err != nil {
		return err
	}

	jw, err = e.Me.TrackingProofFor(e.Them)
	if err != nil {
		return err
	}

	if key, err = G.Keyrings.GetSecretKey("tracking signature"); err != nil {
		return
	} else if key == nil {
		err = NoSecretKeyError{}
		return
	}

	if tmp, err = jw.Marshal(); err != nil {
		return
	}

	if sig, sigid, err = SimpleSign(tmp, *key); err != nil {
		return
	}

	_, err = G.API.Post(ApiArg{
		Endpoint:    "follow",
		NeedSession: true,
		Args: HttpArgs{
			"sig_id_base":  S{sigid.ToString(false)},
			"sig_id_short": S{sigid.ToShortId()},
			"sig":          S{sig},
			"uid":          S{e.Them.GetUid().ToString()},
			"type":         S{"track"},
		},
	})

	return
}

//=====================================================================
