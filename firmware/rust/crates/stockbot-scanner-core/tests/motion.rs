//! Docked jostle guard threshold and hold (#744, part 2).

use stockbot_scanner_core::motion::{
    exceeds_motion_threshold, MotionGuard, MOTION_HOLD_MS, MOTION_THRESHOLD_MG,
};

#[test]
fn threshold_is_0_08_g() {
    assert_eq!(MOTION_THRESHOLD_MG, 80);
}

#[test]
fn just_under_0_08_g_is_still() {
    assert!(!exceeds_motion_threshold([0, 0, 1079]), "+0.079 g");
    assert!(!exceeds_motion_threshold([0, 0, 921]), "-0.079 g");
    assert!(
        !exceeds_motion_threshold([0, -1079, 0]),
        "+0.079 g on another axis"
    );
}

#[test]
fn at_0_08_g_is_moving() {
    assert!(exceeds_motion_threshold([0, 0, 1080]), "+0.08 g");
    assert!(exceeds_motion_threshold([0, 0, 920]), "-0.08 g");
    assert!(
        exceeds_motion_threshold([-1080, 0, 0]),
        "+0.08 g on another axis"
    );
}

#[test]
fn uses_the_vector_length_not_one_axis() {
    // 600² + 800² = 1000²: resting, tilted.
    assert!(!exceeds_motion_threshold([600, 0, 800]));
    // 648² + 864² = 1080².
    assert!(exceeds_motion_threshold([648, 0, 864]));
    // 0.08 g sideways on a resting board: ‖a‖ ≈ 1.0032 g.
    assert!(!exceeds_motion_threshold([80, 0, 1000]));
    assert!(exceeds_motion_threshold([0, 0, 0]), "free fall");
}

#[test]
fn guard_holds_after_the_last_moving_sample() {
    let mut guard = MotionGuard::default();
    assert!(!guard.is_moving(1000));
    guard.sample([0, 0, 1000], 1000);
    assert!(!guard.is_moving(1000));
    guard.sample([0, 0, 1200], 2000);
    guard.sample([0, 0, 1000], 2010);
    assert!(guard.is_moving(2000 + MOTION_HOLD_MS - 1));
    assert!(!guard.is_moving(2000 + MOTION_HOLD_MS));
}
