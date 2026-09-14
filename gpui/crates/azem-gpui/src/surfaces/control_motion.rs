use gpui::{
    AnimationExt, App, Div, Element, Interpolate, Rgba, SpringAnimation, SpringAnimationElement,
    SpringConfig, SpringPlayback, Stateful, prelude::*,
};

use crate::theme::AppearancePreferences;

const CONTROL_SPRING: SpringConfig = SpringConfig::new(900., 60., 1.);

pub(crate) fn control_animation(target: f32, cx: &App) -> SpringAnimation<f32> {
    SpringAnimation::new(CONTROL_SPRING)
        .to(target)
        .with_epsilon(0.005)
        .playback(if AppearancePreferences::current(cx).reduced_motion {
            SpringPlayback::Completed
        } else {
            SpringPlayback::Running
        })
}

pub(crate) trait SelectionMotion: Sized {
    fn animate_selection(
        self,
        selected: bool,
        off: Rgba,
        on: Rgba,
        cx: &App,
    ) -> SpringAnimationElement<Self>;
}

impl SelectionMotion for Stateful<Div> {
    fn animate_selection(
        self,
        selected: bool,
        off: Rgba,
        on: Rgba,
        cx: &App,
    ) -> SpringAnimationElement<Self> {
        let id = Element::id(&self).expect("stateful controls have an element ID");
        self.with_spring(
            id,
            control_animation(if selected { 1. } else { 0. }, cx),
            move |control, phase| control.bg(Rgba::interpolate(off, on, phase)),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use gpui::SpringState;

    #[test]
    fn control_motion_moves_both_ways_and_settles_without_bounce() {
        let initial = SpringState {
            position: 0.,
            velocity: 0.,
        };
        let halfway = CONTROL_SPRING.step(initial, 1., 0.1);
        assert!(halfway.position > 0. && halfway.position < 1.);
        assert_eq!(
            CONTROL_SPRING.step(halfway, 0., 0.).position,
            halfway.position
        );
        let reversed = CONTROL_SPRING.step(halfway, 0., 0.1);
        assert!(reversed.position >= 0. && reversed.position < halfway.position);
        for target in [0., 1.] {
            let settled = CONTROL_SPRING.step(halfway, target, 0.3);
            assert!(CONTROL_SPRING.is_settled(settled, target, 0.005));
            assert!((0. ..=1.).contains(&settled.position));
        }
    }
}
