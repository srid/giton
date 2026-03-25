# Single-step mode tests

# Clean up any justfile/ci.just from multi-step tests
rm -f "$TEST_REPO/justfile" "$TEST_REPO/ci.just"
git add -A
git commit -q -m "clean up" --allow-empty
SHA=$(git rev-parse HEAD)

# Basic command succeeds
run_localci --sha "$SHA" -n test -- echo hello
if [[ $RC -eq 0 ]] && echo "$OUT" | grep -q "hello"; then
  pass "basic command succeeds and shows output"
else
  fail "basic command succeeds and shows output (rc=$RC)"
fi

# Command failure propagates exit code
run_localci --sha "$SHA" -n test -- false
if [[ $RC -ne 0 ]]; then
  pass "command failure propagates exit code"
else
  fail "command failure propagates exit code (rc=$RC)"
fi

# Default --name to command basename
true > "$GH_CALL_LOG"
run_localci --sha "$SHA" -- echo hello
if grep -q "localci/echo" "$GH_CALL_LOG"; then
  pass "--name defaults to command basename"
else
  fail "--name defaults to command basename"
fi

# No command + no justfile ci module = error
run_localci --sha "$SHA"
if [[ $RC -ne 0 ]]; then
  pass "no command and no justfile ci module exits with error"
else
  fail "no command and no justfile ci module exits with error"
fi

# Not in git repo
cd /tmp
run_localci --sha "$SHA" -n test -- echo hello
if [[ $RC -ne 0 ]] && echo "$OUT" | grep -qi "git repository"; then
  pass "not in git repo fails"
else
  fail "not in git repo fails (rc=$RC)"
fi
cd "$TEST_REPO"
