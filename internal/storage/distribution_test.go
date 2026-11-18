package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func distributionTargets(delivery string) []TargetJob {
	jobs := []TargetJob{}
	for _, account := range []int64{1, 2, 3} {
		jobs = append(jobs, TargetJob{ID: fmt.Sprintf("%s-%d", delivery, account), DeliveryID: delivery, ProfileID: "strategy", ContractID: "mnq", AccountID: account, Policy: "frozen", ClaimedAt: time.Now().UTC()})
	}
	return jobs
}
func TestDistributionRotationSkipsUnavailableAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "execution.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	all := map[int64]bool{1: true, 2: true, 3: true}
	for n, want := range []int64{1, 2, 3, 1} {
		j, err := s.ClaimRoundRobin("manager", distributionTargets(fmt.Sprint(n)), all)
		if err != nil || j.AccountID != want {
			t.Fatal(n, j.AccountID, err)
		}
	}
	if _, err = s.ClaimRoundRobin("manager", distributionTargets("0"), all); !errors.Is(err, ErrDuplicate) {
		t.Fatal("duplicate was not rejected", err)
	}
	j, err := s.ClaimRoundRobin("manager", distributionTargets("skip"), map[int64]bool{3: true})
	if err != nil || j.AccountID != 3 {
		t.Fatal("unavailable accounts not skipped", j, err)
	}
	j, err = s.ClaimRoundRobin("manager", distributionTargets("none"), nil)
	if err != nil || j.AccountID != 0 || j.State != "blocked" || j.FinishedAt.IsZero() {
		t.Fatal("no-ready outcome not durable", j, err)
	}
	if _, err = s.ClaimRoundRobin("manager", distributionTargets("none"), all); !errors.Is(err, ErrDuplicate) {
		t.Fatal("blocked alert replayed", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err = s.ClaimRoundRobin("manager", distributionTargets("after-restart"), all)
	if err != nil || j.AccountID != 1 {
		t.Fatal("rotation reset or blocked alert advanced cursor", j, err)
	}
	if err = s.ClaimTargets(distributionTargets("after-restart")); !errors.Is(err, ErrDuplicate) {
		t.Fatal("mode change expanded a prior distribution", err)
	}
	if err = s.ClaimTargets(distributionTargets("copy")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimRoundRobin("manager", distributionTargets("copy"), all); !errors.Is(err, ErrDuplicate) {
		t.Fatal("mode change duplicated a copied alert", err)
	}
}
func TestConcurrentDistributionClaimsAreAtomicAndManagerScoped(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	results := make(chan TargetJob, 30)
	failures := make(chan error, 30)
	for n := 0; n < 30; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			j, err := s.ClaimRoundRobin("manager", distributionTargets(fmt.Sprint(n)), map[int64]bool{1: true, 2: true, 3: true})
			if err != nil {
				failures <- err
			} else {
				results <- j
			}
		}(n)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	counts := map[int64]int{}
	for j := range results {
		counts[j.AccountID]++
	}
	for account := int64(1); account <= 3; account++ {
		if counts[account] != 10 {
			t.Fatal("rotation was not atomic", counts)
		}
	}
	j, err := s.ClaimRoundRobin("other-manager", distributionTargets("other"), map[int64]bool{1: true, 2: true, 3: true})
	if err != nil || j.AccountID != 1 {
		t.Fatal("unrelated manager inherited rotation", j, err)
	}
}
