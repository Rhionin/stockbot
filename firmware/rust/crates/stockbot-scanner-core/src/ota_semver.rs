//! SemVer 2.0.0 validation for OTA manifest `version` (#445 S2).

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

/// True when the offered version should install (differs from running, build metadata ignored).
pub fn versions_differ(offered: &str, running: &str) -> bool {
    if !validate_semver2(offered) || !validate_semver2(running) {
        return true;
    }
    version_without_build(offered) != version_without_build(running)
}

fn version_without_build(v: &str) -> &str {
    v.split('+').next().unwrap_or(v)
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
