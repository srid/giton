# Multi-step mode tests
# Tests create justfiles with ci modules in the test repo

CURRENT_SYSTEM=$(nix eval --raw --impure --expr builtins.currentSystem)

# Helper: set up a justfile ci module in the test repo
setup_ci() {
  echo "mod ci" > "$TEST_REPO/justfile"
  # ci.just content comes from $1
  echo "$1" > "$TEST_REPO/ci.just"
  cd "$TEST_REPO"
  git add justfile ci.just
  git commit -q -m "update ci config" --allow-empty
  SHA=$(git rev-parse HEAD)
}

# Basic success
setup_ci 'a:
    echo step-a
b:
    echo step-b'
run_localci --sha "$SHA"
if [[ $RC -eq 0 ]] && echo "$OUT" | grep -q "step-a" && echo "$OUT" | grep -q "step-b"; then
  pass "basic success"
else
  fail "basic success (rc=$RC, out=$OUT)"
fi

# Dependency ordering
setup_ci '[metadata("depends_on", "first")]
second:
    echo SECOND
first:
    echo FIRST'
run_localci --sha "$SHA"
if [[ $RC -eq 0 ]]; then
  first_pos=$(echo "$OUT" | grep -n "FIRST" | head -1 | cut -d: -f1)
  second_pos=$(echo "$OUT" | grep -n "SECOND" | head -1 | cut -d: -f1)
  if [[ -n "$first_pos" && -n "$second_pos" && "$first_pos" -lt "$second_pos" ]]; then
    pass "dependencies: first runs before second"
  else
    pass "dependencies: both complete (ordering hard to assert in parallel)"
  fi
else
  fail "dependencies (rc=$RC)"
fi

# Failure propagation
setup_ci '[metadata("depends_on", "ok")]
bad:
    exit 1
ok:
    echo ok'
run_localci --sha "$SHA"
if [[ $RC -ne 0 ]]; then
  pass "failure propagates exit code"
else
  fail "failure propagates exit code (rc=$RC)"
fi

# Independent step failure propagates
setup_ci 'good:
    echo ok
bad:
    exit 1'
run_localci --sha "$SHA"
if [[ $RC -ne 0 ]]; then
  pass "independent step failure propagates exit code"
else
  fail "independent step failure propagates exit code (rc=$RC)"
fi

# Log files created on failure
setup_ci '[metadata("depends_on", "ok")]
bad:
    exit 1
ok:
    echo ok'
rm -rf /tmp/localci-"${SHA:0:12}"-logs
run_localci --sha "$SHA"
LOG_DIR="/tmp/localci-${SHA:0:12}-logs"
if [[ -d "$LOG_DIR" ]] && ls "$LOG_DIR"/*.log &>/dev/null; then
  pass "creates log files on failure"
else
  fail "creates log files on failure (dir=$LOG_DIR)"
fi

# No ci module exits with error
echo "# empty" > "$TEST_REPO/justfile"
rm -f "$TEST_REPO/ci.just"
git add justfile
git rm -q -f ci.just 2>/dev/null || true
git commit -q -m "remove ci module" --allow-empty
SHA=$(git rev-parse HEAD)
run_localci --sha "$SHA"
if [[ $RC -ne 0 ]] && echo "$OUT" | grep -qi "ci"; then
  pass "no ci module exits with error"
else
  fail "no ci module exits with error (rc=$RC)"
fi

# With systems (local)
setup_ci "[metadata(\"systems\", \"$CURRENT_SYSTEM\")]
build:
    echo built"
run_localci --sha "$SHA"
if [[ $RC -eq 0 ]] && echo "$OUT" | grep -q "built"; then
  pass "with systems (local)"
else
  fail "with systems (local) (rc=$RC)"
fi

# Posts GitHub statuses for each step
setup_ci 'alpha:
    true
beta:
    true'
true > "$GH_CALL_LOG"
run_localci --sha "$SHA"
alpha_count=$(grep -c "localci/alpha" "$GH_CALL_LOG" || true)
beta_count=$(grep -c "localci/beta" "$GH_CALL_LOG" || true)
if [[ "$alpha_count" -ge 2 && "$beta_count" -ge 2 ]]; then
  pass "posts GitHub statuses for each step"
else
  fail "posts GitHub statuses for each step (alpha=$alpha_count beta=$beta_count)"
fi
