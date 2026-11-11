//! SemVer 2.0.0 validation and precedence comparison (parity with Go `internal/firmware`).

/// Returns true when `v` is valid SemVer 2.0.0 without a leading `v` (Go `validateSemVer2`).
pub fn validate_semver2(v: &str) -> bool {
    if v.is_empty() || v.starts_with('v') {
        return false;
    }
    let mut core = v;
    let mut build = "";
    if let Some(i) = v.find('+') {
        build = &v[i + 1..];
        core = &v[..i];
        if build.is_empty() {
            return false;
        }
    }
    let mut pre = "";
    if let Some(i) = core.find('-') {
        pre = &core[i + 1..];
        core = &core[..i];
        if pre.is_empty() {
            return false;
        }
    }
    let parts: Vec<&str> = core.split('.').collect();
    if parts.len() != 3 {
        return false;
    }
    for part in &parts {
        if !semver_numeric_id(part) {
            return false;
        }
    }
    if !pre.is_empty() && !semver_prerelease_identifier_list(pre) {
        return false;
    }
    if !build.is_empty() && !semver_build_identifier_list(build) {
        return false;
    }
    true
}

fn semver_numeric_id(s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    if s.len() > 1 && s.starts_with('0') {
        return false;
    }
    s.bytes().all(|c| c.is_ascii_digit())
}

fn semver_prerelease_identifier_list(s: &str) -> bool {
    for part in s.split('.') {
        if part.is_empty() {
            return false;
        }
        if is_all_digits(part) {
            if !semver_numeric_id(part) {
                return false;
            }
        } else if !semver_alphanumeric_id(part) {
            return false;
        }
    }
    true
}

fn semver_build_identifier_list(s: &str) -> bool {
    for part in s.split('.') {
        if part.is_empty() || !semver_alphanumeric_id(part) {
            return false;
        }
    }
    true
}

fn semver_alphanumeric_id(s: &str) -> bool {
    s.bytes()
        .all(|c| c.is_ascii_digit() || matches!(c, b'A'..=b'Z' | b'a'..=b'z' | b'-'))
}

fn is_all_digits(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_digit())
}

#[derive(Debug, Clone)]
struct SemverPrec {
    major: String,
    minor: String,
    patch: String,
    pre: Vec<SemverPrecId>,
}

#[derive(Debug, Clone)]
struct SemverPrecId {
    numeric: bool,
    text: String,
}

fn parse_semver_precedence(v: &str) -> Option<SemverPrec> {
    if !validate_semver2(v) {
        return None;
    }
    let mut trimmed = v;
    if let Some(i) = trimmed.find('+') {
        trimmed = &trimmed[..i];
    }
    let mut pre = "";
    let mut core = trimmed;
    if let Some(i) = trimmed.find('-') {
        pre = &trimmed[i + 1..];
        core = &trimmed[..i];
    }
    let parts: Vec<&str> = core.split('.').collect();
    if parts.len() != 3 {
        return None;
    }
    for part in &parts {
        if !semver_numeric_id(part) {
            return None;
        }
    }
    let mut ids = Vec::new();
    if !pre.is_empty() {
        for part in pre.split('.') {
            if is_all_digits(part) {
                ids.push(SemverPrecId {
                    numeric: true,
                    text: part.to_string(),
                });
            } else {
                ids.push(SemverPrecId {
                    numeric: false,
                    text: part.to_string(),
                });
            }
        }
    }
    Some(SemverPrec {
        major: parts[0].to_string(),
        minor: parts[1].to_string(),
        patch: parts[2].to_string(),
        pre: ids,
    })
}

/// Compares two SemVer 2 strings ignoring build metadata: -1 if a<b, 0 if equal, 1 if a>b.
pub fn compare_semver_precedence(a: &str, b: &str) -> i32 {
    let (Some(ap), Some(bp)) = (parse_semver_precedence(a), parse_semver_precedence(b)) else {
        return a.cmp(b) as i32;
    };
    let c = compare_numeric_identifiers(&ap.major, &bp.major);
    if c != 0 {
        return c;
    }
    let c = compare_numeric_identifiers(&ap.minor, &bp.minor);
    if c != 0 {
        return c;
    }
    let c = compare_numeric_identifiers(&ap.patch, &bp.patch);
    if c != 0 {
        return c;
    }
    compare_prerelease(&ap.pre, &bp.pre)
}

/// True when `a` and `b` have equal precedence (build metadata ignored).
pub fn semver_equal_precedence(a: &str, b: &str) -> bool {
    compare_semver_precedence(a, b) == 0
}

/// True when the offered version should be treated as different from the running version.
pub fn versions_differ(offered: &str, running: &str) -> bool {
    !semver_equal_precedence(offered, running)
}

fn compare_prerelease(a: &[SemverPrecId], b: &[SemverPrecId]) -> i32 {
    if a.is_empty() && b.is_empty() {
        return 0;
    }
    if a.is_empty() {
        return 1;
    }
    if b.is_empty() {
        return -1;
    }
    let n = a.len().min(b.len());
    for i in 0..n {
        let c = compare_prec_id(&a[i], &b[i]);
        if c != 0 {
            return c;
        }
    }
    match a.len().cmp(&b.len()) {
        std::cmp::Ordering::Equal => 0,
        std::cmp::Ordering::Greater => 1,
        std::cmp::Ordering::Less => -1,
    }
}

fn compare_prec_id(a: &SemverPrecId, b: &SemverPrecId) -> i32 {
    match (a.numeric, b.numeric) {
        (true, true) => compare_numeric_identifiers(&a.text, &b.text),
        (true, false) => -1,
        (false, true) => 1,
        (false, false) => a.text.cmp(&b.text) as i32,
    }
}

fn compare_numeric_identifiers(a: &str, b: &str) -> i32 {
    if a.len() != b.len() {
        return if a.len() < b.len() { -1 } else { 1 };
    }
    a.cmp(b) as i32
}
