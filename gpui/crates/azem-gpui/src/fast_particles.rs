use std::time::{Duration, Instant};

use gpui::{
    Bounds, Context, IntoElement, Render, Window, canvas, div, fill, point, prelude::*, px, rgba,
    size,
};

const PARTICLE_COUNT: usize = 8;
const CYCLE_DURATION: Duration = Duration::from_secs(4);
const FRAME_INTERVAL: Duration = Duration::from_nanos(1_000_000_000 / 60);

pub fn should_animate(
    visible: bool,
    fast: bool,
    reduced_motion: bool,
    window_active: bool,
) -> bool {
    visible && fast && !reduced_motion && window_active
}

/// Sparse, constant-speed dots with display-synchronized 60 fps updates.
pub struct FastParticles {
    started: Option<Instant>,
    phase: f32,
    frame: u128,
    frame_pending: bool,
}

impl FastParticles {
    pub fn new() -> Self {
        Self {
            started: None,
            phase: 0.,
            frame: 0,
            frame_pending: false,
        }
    }

    pub fn set_active(&mut self, active: bool, cx: &mut Context<Self>) {
        if active == self.started.is_some() {
            return;
        }
        if active {
            self.started = Some(Instant::now());
        } else if let Some(started) = self.started.take() {
            self.phase = (self.phase + particle_phase(started.elapsed())).fract();
        }
        self.frame = 0;
        tracing::trace!(target: "azem_gpui::fast_particles", %active);
        cx.notify();
    }

    fn request_frame(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.frame_pending {
            return;
        }
        self.frame_pending = true;
        let owner = cx.entity().downgrade();
        window.on_next_frame(move |window, cx| {
            let _ = owner.update(cx, |this, cx| {
                this.frame_pending = false;
                let Some(started) = this.started else {
                    return;
                };
                let frame = frame_index(started.elapsed());
                if frame != this.frame {
                    this.frame = frame;
                    cx.notify();
                } else {
                    // Skip GPU redraws between 60 Hz buckets on faster displays.
                    this.request_frame(window, cx);
                }
            });
        });
    }
}

fn frame_index(elapsed: Duration) -> u128 {
    elapsed.as_nanos() / FRAME_INTERVAL.as_nanos()
}

fn particle_phase(elapsed: Duration) -> f32 {
    (elapsed.as_nanos() % CYCLE_DURATION.as_nanos()) as f32 / CYCLE_DURATION.as_nanos() as f32
}

fn particle_position(index: usize, phase: f32, width: f32, height: f32) -> (f32, f32) {
    let x = (phase + index as f32 / PARTICLE_COUNT as f32).fract() * width;
    let y = height * [0.55, 0.30, 0.18, 0.62, 0.42, 0.22, 0.78, 0.5][index];
    (x, y)
}

impl Render for FastParticles {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if self.started.is_some() {
            self.request_frame(window, cx);
        }
        let phase = self
            .started
            .map(|started| (self.phase + particle_phase(started.elapsed())).fract())
            .unwrap_or(self.phase);
        tracing::trace!(target: "azem_gpui::fast_particles", phase = "tick", progress = phase, particles = PARTICLE_COUNT);
        div().absolute().inset_0().overflow_hidden().child(
            canvas(
                |_, _, _| (),
                move |bounds, _, window, _| {
                    let width = f32::from(bounds.size.width);
                    let height = f32::from(bounds.size.height);
                    for index in 0..PARTICLE_COUNT {
                        let (x, y) = particle_position(index, phase, width, height);
                        let diameter = [1.5, 2., 1.6, 1.8, 2.2, 1.6, 2.8, 2.4][index];
                        let alpha = [0x80, 0xb0, 0x90, 0x70, 0xa0, 0x70, 0x60, 0x90][index];
                        window.paint_quad(
                            fill(
                                Bounds::new(
                                    bounds.origin
                                        + point(px(x - diameter / 2.), px(y - diameter / 2.)),
                                    size(px(diameter), px(diameter)),
                                ),
                                rgba(0xffffff00 | alpha),
                            )
                            .corner_radii(px(diameter / 2.)),
                        );
                    }
                },
            )
            .size_full(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn particles_follow_display_frames_without_a_lifetime_limit() {
        let implementation = include_str!("fast_particles.rs")
            .split("#[cfg(test)]")
            .next()
            .unwrap();
        assert!(implementation.contains("window.on_next_frame("));
        assert!(!implementation.contains("BURST_FRAMES"));
        assert!(!implementation.contains("timer("));
    }

    #[test]
    fn faster_displays_do_not_trigger_more_than_sixty_particle_updates_per_second() {
        for refresh_rate in [60, 120, 144, 165, 240] {
            let mut last_frame = 0;
            let mut updates = 0;
            for tick in 1..=refresh_rate * 5 {
                let elapsed = Duration::from_nanos(tick * 1_000_000_000 / refresh_rate);
                let frame = frame_index(elapsed);
                if frame != last_frame {
                    last_frame = frame;
                    updates += 1;
                }
            }
            assert_eq!(updates, 300, "{refresh_rate} Hz display");
        }
    }

    #[test]
    fn particles_only_run_for_a_visible_active_fast_picker_without_reduced_motion() {
        for flags in 0..16 {
            assert_eq!(
                should_animate(
                    flags & 1 != 0,
                    flags & 2 != 0,
                    flags & 4 != 0,
                    flags & 8 != 0
                ),
                flags == 0b1011,
            );
        }
    }

    #[test]
    fn particle_budget_and_positions_stay_bounded() {
        assert_eq!(PARTICLE_COUNT, 8);
        assert_eq!(CYCLE_DURATION, Duration::from_secs(4));
        for width in [1., 140., 260.] {
            for tick in 0..240 {
                let phase = particle_phase(Duration::from_secs_f64(tick as f64 / 60.));
                for index in 0..PARTICLE_COUNT {
                    let (x, y) = particle_position(index, phase, width, 16.);
                    assert!((0.0..width).contains(&x));
                    assert!((0.0..16.).contains(&y));
                }
            }
        }
        for cycle in [0, 1, 10, 100_000] {
            for step in 0..10 {
                let elapsed = CYCLE_DURATION * cycle + Duration::from_millis(step * 400);
                let phase = particle_phase(elapsed);
                assert!((phase - step as f32 / 10.).abs() < 0.0001);
                let (x, _) = particle_position(0, phase, 260., 16.);
                assert!((x - step as f32 * 26.).abs() < 0.0001);
            }
        }
    }

    #[test]
    fn slider_reveal_increases_particle_count_with_progress() {
        let mut previous = 0;
        for step in 0..=10 {
            let progress = step as f32 / 10.;
            let visible = (0..PARTICLE_COUNT)
                .filter(|index| particle_position(*index, 0., 1., 1.).0 < progress)
                .count();
            assert!(visible >= previous, "{progress}: {visible} < {previous}");
            previous = visible;
        }
        assert_eq!(previous, PARTICLE_COUNT);
    }
}
