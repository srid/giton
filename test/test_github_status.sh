# GitHub status posting tests

CURRENT_SYSTEM=$(nix eval --raw --impure --expr builtins.currentSystem)

# Posts pending + success
true > "$GH_CALL_LOG"
run_giton --sha "$SHA" -n test -- true
if grep -q "pending" "$GH_CALL_LOG" && grep -q "success" "$GH_CALL_LOG"; then
  pass "posts pending and success statuses"
else
  fail "posts pending and success statuses"
fi

# Posts failure status
true > "$GH_CALL_LOG"
run_giton --sha "$SHA" -n test -- false
if grep -q "failure" "$GH_CALL_LOG"; then
  pass "posts failure status on error"
else
  fail "posts failure status on error"
fi

# Context without --system: giton/<name>
true > "$GH_CALL_LOG"
run_giton --sha "$SHA" -n mycheck -- true
if grep -q "giton/mycheck" "$GH_CALL_LOG"; then
  pass "context: giton/<name> without --system"
else
  fail "context: giton/<name> without --system"
fi

# Context with --system: giton/<name>/<system>
true > "$GH_CALL_LOG"
run_giton --sha "$SHA" -s "$CURRENT_SYSTEM" -n mycheck -- true
if grep -q "giton/mycheck/$CURRENT_SYSTEM" "$GH_CALL_LOG"; then
  pass "context: giton/<name>/<system> with --system"
else
  fail "context: giton/<name>/<system> with --system"
fi
