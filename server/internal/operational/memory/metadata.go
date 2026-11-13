package memory

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var metadataLog = logger.New("cao:operational:memory:metadata")

func (s *Store) AddMembers(ctx context.Context, name string, members ...string) (int64, error) {
	if err := s.enter(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	if err := s.name(name); err != nil {
		return 0, err
	}
	if err := s.name(members...); err != nil {
		return 0, err
	}
	changes := make(map[recordKey]int64)
	for _, member := range members {
		if _, exists := s.members[name][member]; !exists {
			changes[recordKey{"member", name, member}] = charge(name, member)
		}
	}
	if err := s.reserve(changes); err != nil {
		return 0, err
	}
	if len(changes) != 0 {
		if s.members[name] == nil {
			s.members[strings.Clone(name)] = make(map[string]struct{})
		}
		for k := range changes {
			s.members[name][strings.Clone(k.field)] = struct{}{}
		}
	}
	return int64(len(changes)), nil
}

func (s *Store) removeMember(name, member string) {
	delete(s.members[name], member)
	s.drop(recordKey{"member", name, member})
	if len(s.members[name]) == 0 {
		delete(s.members, name)
	}
}

func (s *Store) RemoveMembers(ctx context.Context, name string, members ...string) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.name(name); err != nil {
		return err
	}
	if err := s.name(members...); err != nil {
		return err
	}
	for _, member := range members {
		s.removeMember(name, member)
	}
	return nil
}

func (s *Store) HasMember(ctx context.Context, name, member string) (bool, error) {
	if err := s.enter(ctx); err != nil {
		return false, err
	}
	defer s.mu.Unlock()
	if err := s.name(name, member); err != nil {
		return false, err
	}
	_, exists := s.members[name][member]
	return exists, nil
}

func (s *Store) MemberCount(ctx context.Context, name string) (int64, error) {
	if err := s.enter(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	if err := s.name(name); err != nil {
		return 0, err
	}
	return int64(len(s.members[name])), nil
}

// ScanMembers offers deterministic, bounded pages. Like operational storage SSCAN, callers
// must tolerate repeats or omissions if membership changes during a scan.
func (s *Store) ScanMembers(ctx context.Context, name, cursor string, count int) ([]string, string, error) {
	if err := s.enter(ctx); err != nil {
		return nil, "", err
	}
	defer s.mu.Unlock()
	if err := s.name(name); err != nil {
		return nil, "", err
	}
	if count <= 0 {
		return nil, "", invalid("scan count must be positive")
	}
	var offset int
	var err error
	if cursor != "" {
		offset, err = strconv.Atoi(cursor)
		if err != nil || offset < 0 || cursor[0] == '-' || cursor[0] == '+' {
			return nil, "", invalid("invalid membership scan cursor")
		}
	}
	all := make([]string, 0, len(s.members[name]))
	for member := range s.members[name] {
		all = append(all, member)
	}
	sort.Strings(all)
	if offset >= len(all) {
		return []string{}, "0", nil
	}
	start := offset
	end := start + min(count, len(all)-start)
	next := "0"
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	return all[start:end:end], next, nil
}

func (s *Store) ReadAttribute(ctx context.Context, name, field string) (string, error) {
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer s.mu.Unlock()
	if err := s.name(name, field); err != nil {
		return "", err
	}
	return s.attributes[name][field], nil
}

func (s *Store) writeAttribute(name, field, value string) error {
	if err := s.reserve(map[recordKey]int64{{"attribute", name, field}: charge(name, field, value)}); err != nil {
		return err
	}
	if s.attributes[name] == nil {
		s.attributes[strings.Clone(name)] = make(map[string]string)
	}
	s.attributes[name][strings.Clone(field)] = strings.Clone(value)
	return nil
}

func (s *Store) WriteAttribute(ctx context.Context, name, field, value string) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.name(name, field); err != nil {
		return err
	}
	if err := s.value(value); err != nil {
		return err
	}
	return s.writeAttribute(name, field, value)
}

func (s *Store) DeleteAttribute(ctx context.Context, name, field string) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.name(name, field); err != nil {
		return err
	}
	delete(s.attributes[name], field)
	s.drop(recordKey{"attribute", name, field})
	if len(s.attributes[name]) == 0 {
		delete(s.attributes, name)
	}
	return nil
}

// Clear removes logical metadata/state and debounce markers, never historical
// identities or recoverable queued work.
func (s *Store) Clear(ctx context.Context, name string) error {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.name(name); err != nil {
		return err
	}
	for member := range s.members[name] {
		s.drop(recordKey{"member", name, member})
	}
	delete(s.members, name)
	for field := range s.attributes[name] {
		s.drop(recordKey{"attribute", name, field})
	}
	delete(s.attributes, name)
	k := recordKey{"debounce", name, ""}
	delete(s.markers, k)
	s.drop(k)
	delete(s.states, name)
	s.drop(recordKey{"state", name, ""})
	return nil
}

// ownerTransferDecision is what one repository's existing owner attribute
// implies for TransferOwners: whether it holds a distinct, previously
// enrolled installation that must lose membership, and if so the old
// installation's logical set name.
type ownerTransferDecision struct {
	transfers bool
	oldSet    string
}

// decideOwnerTransfer inspects one repository's previous owner attribute
// value and reports whether its membership in the old installation's
// logical set must be removed. It is a pure function extracted from
// TransferOwners's loop so the previous-owner parsing and eligibility rules
// (a blank or non-positive value, or one already matching installation) are
// independently testable against constructed attribute values, without a
// Store.
func decideOwnerTransfer(installationPrefix, installationSuffix string, installation int64, previous string) ownerTransferDecision {
	oldID, err := strconv.ParseInt(previous, 10, 64)
	if err != nil || oldID <= 0 || oldID == installation {
		return ownerTransferDecision{}
	}
	return ownerTransferDecision{transfers: true, oldSet: installationPrefix + previous + installationSuffix}
}

// TransferOwners changes ownership attributes and removes stale memberships.
// Enrollment separately adds the new installation/global memberships. Prefix
// and suffix construct a logical name, not a physical storage key.
func (s *Store) TransferOwners(ctx context.Context, owners, installationPrefix, installationSuffix string, installation int64, repositories []string) (int, error) {
	if err := s.enter(ctx); err != nil {
		return 0, err
	}
	defer s.mu.Unlock()
	if err := s.name(owners); err != nil {
		return 0, err
	}
	if err := s.namespace(installationPrefix); err != nil {
		return 0, err
	}
	if err := s.namespace(installationSuffix); err != nil {
		return 0, err
	}
	if installation <= 0 {
		return 0, invalid("installation ID must be positive")
	}
	if err := s.name(repositories...); err != nil {
		return 0, err
	}
	owner := strconv.FormatInt(installation, 10)
	changes := make(map[recordKey]int64)
	remove := make(map[recordKey]struct{})
	transferred := 0
	for _, repo := range repositories {
		key := recordKey{"attribute", owners, repo}
		if _, duplicate := changes[key]; duplicate {
			continue
		}
		decision := decideOwnerTransfer(installationPrefix, installationSuffix, installation, s.attributes[owners][repo])
		if decision.transfers {
			if err := s.name(decision.oldSet); err != nil {
				return 0, err
			}
			member := recordKey{"member", decision.oldSet, repo}
			changes[member] = 0
			remove[member] = struct{}{}
			transferred++
		}
		changes[key] = charge(owners, repo, owner)
	}
	if err := s.reserve(changes); err != nil {
		return 0, err
	}
	for k := range remove {
		s.removeMember(k.name, k.field)
	}
	if len(repositories) != 0 && s.attributes[owners] == nil {
		s.attributes[strings.Clone(owners)] = make(map[string]string)
	}
	for k := range changes {
		if k.kind == "attribute" {
			s.attributes[owners][strings.Clone(k.field)] = owner
		}
	}
	metadataLog.Printf("owner transfer completed repositories=%d transferred=%d", len(repositories), transferred)
	return transferred, nil
}
