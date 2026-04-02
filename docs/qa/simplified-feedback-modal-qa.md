# QA Guide: Simplified Feedback Modal with LocalStorage Persistence

**Feature ID**: `simplified-feedback-modal-with-localstorage-persistence`
**Cycle**: `feature-implementation` — Step 5/5 (human-qa)
**Judge Score**: 9.0/10
**Priority**: 9 | **Milestone**: v0.2 Self-Hosting

---

## Overview

The Quick Feedback modal (triggered by pressing `F` or clicking the feedback button) has been redesigned from a multi-field form to a minimal single-textarea modal. Content auto-saves to localStorage so drafts survive page refreshes and modal close/reopen cycles.

## Test Environment

1. Start the server: `bin/tillr serve --port 3847`
2. Open browser to `http://localhost:3847`
3. Ensure you're on any page (Dashboard works fine)

---

## Test Cases

### TC-1: Modal Appearance
**Steps:**
1. Press `F` or click the feedback button (bottom-right corner)
2. Observe the modal

**Expected:**
- [ ] Modal is roughly 60% of viewport width and 60% of viewport height
- [ ] Modal has a title bar reading "Quick Feedback" with an `×` close button
- [ ] Below the title bar is a single textarea with placeholder text
- [ ] Below the textarea is a "Submit" button
- [ ] There are NO extra fields (no title input, no type selector, no description field)
- [ ] Modal is centered on screen with a dark backdrop

### TC-2: Close Mechanisms
**Steps:**
1. Open the modal (`F` key)
2. Type some text
3. Close using the `×` button
4. Reopen the modal
5. Close by clicking the backdrop
6. Reopen the modal
7. Close by pressing `Escape`

**Expected:**
- [ ] All three close methods work: `×` button, backdrop click, Escape key
- [ ] Text content is preserved across all close/reopen cycles

### TC-3: LocalStorage Persistence — Keystroke Save
**Steps:**
1. Open the modal
2. Type "Hello world"
3. Open browser DevTools → Application → Local Storage
4. Look for key `tillr_feedback_draft`

**Expected:**
- [ ] The localStorage entry appears with value "Hello world"
- [ ] Value updates as you continue typing (debounced ~300ms)

### TC-4: LocalStorage Persistence — Page Refresh
**Steps:**
1. Open the modal and type "Draft text that should survive"
2. Close the modal
3. Refresh the page (F5 or Ctrl+R)
4. Open the modal again (`F` key)

**Expected:**
- [ ] The textarea contains "Draft text that should survive"
- [ ] No data loss on refresh

### TC-5: LocalStorage Persistence — Navigation
**Steps:**
1. On Dashboard, open the modal and type "Navigation test"
2. Close the modal
3. Navigate to a different page (Features, Roadmap, etc.)
4. Open the modal again

**Expected:**
- [ ] The textarea still contains "Navigation test"

### TC-6: Submit Behavior — Title Extraction
**Steps:**
1. Open the modal
2. Type:
   ```
   My Feature Title
   This is the description that goes below.
   It can be multiple lines.
   ```
3. Click "Submit"

**Expected:**
- [ ] Feedback is submitted successfully (toast notification appears)
- [ ] First line "My Feature Title" becomes the feedback title
- [ ] Remaining lines become the description
- [ ] Modal closes after submit
- [ ] localStorage is cleared (check DevTools)

### TC-7: Submit Behavior — Single Line
**Steps:**
1. Open the modal
2. Type just: "Quick note about something"
3. Click "Submit"

**Expected:**
- [ ] Title is "Quick note about something"
- [ ] Description is empty (or the same as title)
- [ ] Submission succeeds

### TC-8: Submit Behavior — Empty
**Steps:**
1. Open the modal with empty textarea
2. Click "Submit"

**Expected:**
- [ ] Either prevents submission or shows validation error
- [ ] Does not create an empty feedback entry

### TC-9: Verify in Ideas Page
**Steps:**
1. After submitting feedback via TC-6
2. Navigate to the Ideas page

**Expected:**
- [ ] New feedback item appears in the list
- [ ] Title matches the first line of your input
- [ ] Type is "feedback"

---

## Regression Checks

- [ ] `F` keyboard shortcut still opens the modal from any page
- [ ] Other keyboard shortcuts (?, j/k, etc.) still work when modal is closed
- [ ] Keyboard shortcuts are disabled while modal is open (typing in textarea)
- [ ] Dark mode styling is correct
- [ ] Light mode styling is correct
- [ ] Modal is responsive on smaller viewports

---

## Approval

To approve this feature:
```bash
tillr qa approve simplified-feedback-modal-with-localstorage-persistence --notes "QA passed"
```

To reject and send back for rework:
```bash
tillr qa reject simplified-feedback-modal-with-localstorage-persistence --notes "Issue: <describe what failed>"
```
