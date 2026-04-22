# MCP Project Management Tools - Comprehensive Testing Guide

## Overview
This document outlines the complete testing methodology for verifying all MCP project management tools are:
1. **Functionally correct** - Each tool performs its intended operation accurately
2. **Error-free** - Handles edge cases and invalid inputs gracefully
3. **Secure** - Cannot be exploited through injection, path traversal, or other attack vectors

---

## Test Categories & Scenarios

### 1. Project Lifecycle Tests (Open/Close)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| PL-001 | Open existing project directory | Success - context set correctly |
| PL-002 | Open non-existent path | Create new project and initialize git |
| PL-003 | Close active project | Context reset, resources freed |
| PL-004 | Open project after close | Fresh context established |

### 2. File Creation Tests (CreateItem)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| FC-001 | Create single file with content | File created at path |
| FC-002 | Create nested directory structure automatically | All parent dirs created |
| FC-003 | Overwrite existing file (overwrite=true) | Content replaced successfully |
| FC-004 | Create without overwrite on existing file | Error or skip, original preserved |
| FC-005 | Create folder with isFolder=true | Directory created, not file |
| FC-006 | Batch create multiple items | All files/folders created atomically |
| FC-007 | Create file in deep nested path | Full path hierarchy created |

### 3. File Reading Tests (GetItem)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| GR-001 | Read text file content | Correct content returned |
| GR-002 | Read binary file in hex format | Properly encoded hex output |
| GR-003 | List directory contents (recursive) | All files listed |
| GR-004 | Get metadata only (length=0) | File stats without content |
| GR-005 | Read specific line range | Correct lines extracted |
| GR-006 | Compare two files with diff | Differences highlighted accurately |

### 4. File Modification Tests (EditItem)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| EM-001 | Replace single occurrence | Only first match replaced |
| EM-002 | Replace all occurrences (count=0) | All matches replaced |
| EM-003 | Delete file successfully | File removed from filesystem |
| EM-004 | Delete non-existent file gracefully | Success returned, no error |
| EM-005 | Compress to archive | Valid archive created |
| EM-006 | Extract from archive | Contents restored correctly |

### 5. Copy Operations Tests (CopyItem)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| CO-001 | Copy file to new location | File duplicated at destination |
| CO-002 | Copy directory recursively | All contents copied |
| CO-003 | Overwrite existing copy (overwrite=true) | Destination replaced |
| CO-004 | Batch copy multiple files | All copies performed successfully |

### 6. Move Operations Tests (MoveItem)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| MV-001 | Rename single file | File moved to new path, old gone |
| MV-002 | Move directory with contents | All contents relocated |
| MV-003 | Batch move multiple items | All moves completed atomically |

### 7. Git Operations Tests (Git)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| GT-001 | Check working tree status | Accurate staged/unstaged info |
| GT-002 | View commit history with log | Correct chronological entries |
| GT-003 | Show unstaged changes diff | Current modifications displayed |
| GT-004 | Stage files for commit (add) | Files marked as staged |
| GT-005 | Create new commit | New commit created on branch |
| GT-006 | List available branches | Correct branch list returned |

### 8. Search Functionality Tests (Search)

| Test ID | Scenario | Expected Result |
|---------|----------|-----------------|
| SR-001 | Find files by name pattern | Matching filenames found |
| SR-002 | Regex search on filenames | Pattern matches identified |
| SR-003 | Grep file contents for text | Lines containing pattern shown |
| SR-004 | Search with extension filter | Only specified file types returned |

---

## Security Test Scenarios

### Path Traversal Prevention
```
Attempt: CreateItem path = "../../../etc/passwd"
Expected: Blocked or resolved within project scope
Verify: File created only in allowed directory tree
```

### Command Injection Prevention
```
Attempt: EditItem oldText = "'; rm -rf /; '"
Expected: Treated as literal string, not executed
Verify: No shell commands executed
```

### Resource Exhaustion Prevention
```
Attempt: CopyItem source = large_file.zip to many destinations
Expected: Rate limited or quota enforced
Verify: System remains responsive
```

### Data Integrity After Operations
```
For each operation verify:
1. Checksum before operation (if applicable)
2. Operation completes successfully
3. Checksum after operation matches expected state
4. No partial/corrupted files left behind
```

---

## Execution Methodology

### Phase 1: Unit Tests (Automated)
- Run `test_mcp_tools.py` with all test cases
- Verify each tool returns expected status codes
- Validate response formats and data structures

### Phase 2: Integration Tests (Manual Verification)
For each tool, perform these steps:

**Test Template:**
1. **Setup**: Create clean test environment
2. **Execute**: Call the target tool with normal inputs
3. **Verify**: Check output matches expected result
4. **Security**: Repeat with malicious/edge-case inputs
5. **Cleanup**: Remove test artifacts

### Phase 3: Stress Tests
- Bulk operations (100+ files)
- Concurrent operations simulation
- Network latency tolerance

---

## Pass/Fail Criteria

### Must PASS (Critical):
- [ ] All security tests (T071, T072)
- [ ] File creation with correct content
- [ ] Project open/close lifecycle
- [ ] Data integrity maintained after all operations

### Should PASS (Important):
- [ ] Batch operation atomicity
- [ ] Error handling for invalid inputs
- [ ] Git operations accuracy

### Nice to Have (Enhancement):
- [ ] Performance benchmarks
- [ ] Memory leak detection
- [ ] Concurrent access safety

---

## Test Report Format

```
═══════════════════════════════════════════════
MCP TOOLS TEST REPORT
Date: YYYY-MM-DD HH:MM:SS UTC
═══════════════════════════════════════════════

CATEGORY: Security Tests (2 tests)
[T071] Path Traversal Prevention:     PASS ✓
[T072] Command Injection Blocking:   PASS ✓

CATEGORY: File Operations (6 tests)
[T011] Create File with Content:     PASS ✓
[T012] Overwrite Existing File:      PASS ✓
...

═══════════════════════════════════════════════
SUMMARY
Total Tests:  XX
Passed:       XX
Failed:        0
Errors:        0
═══════════════════════════════════════════════
OVERALL RESULT: PASS ✗
═══════════════════════════════════════════════
```
