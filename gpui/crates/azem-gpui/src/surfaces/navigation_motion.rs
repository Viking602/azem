use std::{cell::RefCell, collections::HashMap, rc::Rc, time::Instant};

use gpui::{
    AnyElement, App, Bounds, Element, ElementId, FlexDirection, GlobalElementId,
    InspectorElementId, IntoElement, LayoutId, Pixels, Style, Window, point, px, size,
};

use crate::theme::AppearancePreferences;
use gpui::prelude::*;

#[derive(Default)]
pub(crate) struct SidebarTreeState {
    pub expanded: HashMap<String, bool>,
    pub session_limits: HashMap<String, usize>,
    heights: Rc<RefCell<HashMap<String, ClipMotion>>>,
}

impl SidebarTreeState {
    pub fn expanded(&self, project: &str, active: bool) -> bool {
        self.expanded.get(project).copied().unwrap_or(active)
    }

    pub fn visible_sessions(&self, project: &str, total: usize) -> usize {
        self.session_limits
            .get(project)
            .copied()
            .unwrap_or(5)
            .min(total)
    }

    pub fn show_more(&mut self, project: &str, total: usize) {
        let next = self
            .visible_sessions(project, total)
            .saturating_add(5)
            .min(total);
        self.session_limits.insert(project.into(), next);
    }

    pub fn forget(&mut self, project: &str) {
        self.expanded.remove(project);
        self.session_limits.remove(project);
        self.heights
            .borrow_mut()
            .remove(&format!("project-{project}"));
        self.heights
            .borrow_mut()
            .remove(&format!("sessions-{project}"));
    }
}

pub(super) fn clip_list(
    key: String,
    limit: Option<f32>,
    content: impl IntoElement,
    tree: &SidebarTreeState,
    cx: &mut gpui::Context<crate::AzemWindow>,
) -> AnyElement {
    let reduced = AppearancePreferences::current(cx).reduced_motion;
    let (height, measured, moving) = {
        let mut heights = tree.heights.borrow_mut();
        let motion = heights.entry(key.clone()).or_default();
        let height = motion.height(limit, reduced, Instant::now());
        (height, motion.measured, motion.row.started.is_some())
    };
    if limit == Some(0.) && measured > 0. && height <= 0. && !moving {
        return gpui::div().h(px(0.)).into_any_element();
    }
    let heights = tree.heights.clone();
    let owner = cx.entity();
    gpui::div()
        .w_full()
        .min_w_0()
        .flex()
        .flex_col()
        .overflow_hidden()
        .when(measured > 0., |body| body.h(px(height)))
        .when(measured == 0., |body| body.opacity(0.))
        .on_children_prepainted(move |bounds, window, cx| {
            if moving {
                window.request_animation_frame();
            }
            if let Some(bounds) = bounds.first() {
                let full_height = f32::from(bounds.size.height);
                let mut heights = heights.borrow_mut();
                let measured = &mut heights.get_mut(&key).unwrap().measured;
                if (*measured - full_height).abs() > 0.5 {
                    *measured = full_height;
                    window.request_animation_frame();
                    let owner = owner.clone();
                    cx.defer(move |cx| owner.update(cx, |_, cx| cx.notify()));
                }
            }
        })
        .child(
            gpui::div()
                .w_full()
                .flex()
                .flex_col()
                .flex_shrink_0()
                .child(content),
        )
        .into_any_element()
}

#[derive(Default)]
struct ClipMotion {
    measured: f32,
    limit: Option<f32>,
    row: RowMotion,
}

impl ClipMotion {
    fn height(&mut self, limit: Option<f32>, reduced: bool, now: Instant) -> f32 {
        let target = limit.map_or(self.measured, |limit| limit.min(self.measured));
        // Nested content owns its animation; animate only changes to this clip.
        if self.limit == limit {
            self.row.target = Some(target);
        }
        self.limit = limit;
        target + self.row.offset(target, reduced, now)
    }
}

#[derive(Default)]
struct RowMotion {
    target: Option<f32>,
    from: f32,
    started: Option<Instant>,
}

impl RowMotion {
    fn offset(&mut self, target: f32, reduced: bool, now: Instant) -> f32 {
        let remaining = self.started.map_or(0., |started| {
            1. - crate::css_ease_out(
                (now.saturating_duration_since(started).as_secs_f32() / 0.24).min(1.),
            )
        });
        let current_offset = self.from * remaining;
        if let Some(previous) = self.target
            && previous != target
        {
            self.from = previous + current_offset - target;
            self.started = Some(now);
        } else if remaining == 0. {
            self.started = None;
        }
        self.target = Some(target);
        if reduced {
            self.started = None;
        }
        if self.started == Some(now) {
            self.from
        } else if self.started.is_some() {
            current_offset
        } else {
            0.
        }
    }
}

// Animate measured positions within each list, excluding scrolling and parent
// motion. Prepainting at the same offset keeps hitboxes and accessibility aligned.
pub(super) struct ReorderList {
    id: ElementId,
    gap: f32,
    children: Vec<(String, AnyElement)>,
}

pub(super) fn reorder_list(
    id: impl Into<ElementId>,
    gap: f32,
    children: impl IntoIterator<Item = (String, AnyElement)>,
) -> ReorderList {
    ReorderList {
        id: id.into(),
        gap,
        children: children.into_iter().collect(),
    }
}

impl IntoElement for ReorderList {
    type Element = Self;
    fn into_element(self) -> Self {
        self
    }
}

impl Element for ReorderList {
    type RequestLayoutState = Vec<LayoutId>;
    type PrepaintState = ();

    fn id(&self) -> Option<ElementId> {
        Some(self.id.clone())
    }
    fn source_location(&self) -> Option<&'static core::panic::Location<'static>> {
        None
    }

    fn request_layout(
        &mut self,
        _: Option<&GlobalElementId>,
        _: Option<&InspectorElementId>,
        window: &mut Window,
        cx: &mut App,
    ) -> (LayoutId, Self::RequestLayoutState) {
        let children: Vec<_> = self
            .children
            .iter_mut()
            .map(|(_, child)| child.request_layout(window, cx))
            .collect();
        let style = Style {
            display: gpui::Display::Flex,
            flex_direction: FlexDirection::Column,
            gap: size(px(0.).into(), px(self.gap).into()),
            ..Style::default()
        };
        (
            window.request_layout(style, children.iter().copied(), cx),
            children,
        )
    }

    fn prepaint(
        &mut self,
        id: Option<&GlobalElementId>,
        _: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        layouts: &mut Self::RequestLayoutState,
        window: &mut Window,
        cx: &mut App,
    ) {
        let reduced = AppearancePreferences::current(cx).reduced_motion;
        let now = Instant::now();
        window.with_element_state(
            id.unwrap(),
            |state: Option<HashMap<String, RowMotion>>, window| {
                let mut previous = state.unwrap_or_default();
                let mut next = HashMap::with_capacity(self.children.len());
                for ((key, child), layout) in self.children.iter_mut().zip(layouts.iter()) {
                    let target = f32::from(window.layout_bounds(*layout).top() - bounds.top());
                    let mut motion = previous.remove(key).unwrap_or_default();
                    let offset = motion.offset(target, reduced, now);
                    if motion.started.is_some() {
                        window.request_animation_frame();
                    }
                    window.with_element_offset(point(px(0.), px(offset)), |window| {
                        child.prepaint(window, cx);
                    });
                    next.insert(key.clone(), motion);
                }
                ((), next)
            },
        );
    }

    fn paint(
        &mut self,
        _: Option<&GlobalElementId>,
        _: Option<&InspectorElementId>,
        _: Bounds<Pixels>,
        _: &mut Self::RequestLayoutState,
        _: &mut (),
        window: &mut Window,
        cx: &mut App,
    ) {
        for (_, child) in &mut self.children {
            child.paint(window, cx);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[test]
    fn reorder_preserves_position_retargets_and_stops() {
        let now = Instant::now();
        let mut motion = RowMotion::default();
        assert_eq!(motion.offset(200., false, now), 0.);
        assert_eq!(motion.offset(0., false, now), 200.);
        let midway = now + Duration::from_millis(100);
        let visible = motion.offset(0., false, midway);
        assert!(visible > 0. && visible < 200.);
        assert_eq!(motion.offset(100., false, midway) + 100., visible);
        assert_eq!(
            motion.offset(100., false, midway + Duration::from_millis(240)),
            0.
        );
        assert!(motion.started.is_none());
        assert_eq!(
            motion.offset(0., true, midway + Duration::from_millis(300)),
            0.
        );
        assert!(motion.started.is_none());
    }
    #[test]
    fn sidebar_expansion_is_per_project_and_clips_animate_in_both_directions() {
        let mut tree = SidebarTreeState::default();
        assert!(tree.expanded("a", true));
        tree.expanded.insert("a".into(), false);
        assert!(!tree.expanded("a", true));
        assert_eq!(tree.visible_sessions("a", 12), 5);
        tree.show_more("a", 12);
        assert_eq!(tree.visible_sessions("a", 12), 10);
        assert_eq!(tree.visible_sessions("b", 12), 5);
        tree.show_more("a", 12);
        assert_eq!(tree.visible_sessions("a", 12), 12);
        tree.show_more("a", 12);
        assert_eq!(tree.visible_sessions("a", 12), 12);
        tree.session_limits.remove("a");
        assert_eq!(tree.visible_sessions("a", 12), 5);
        assert_eq!(tree.visible_sessions("empty", 0), 0);
        assert_eq!(tree.visible_sessions("short", 3), 3);
        tree.show_more("a", 12);
        let now = Instant::now();
        let mut clip = ClipMotion {
            measured: 400.,
            limit: Some(191.),
            ..Default::default()
        };
        assert_eq!(clip.height(Some(191.), false, now), 191.);
        assert_eq!(clip.height(None, false, now), 191.);
        assert_eq!(
            clip.height(None, false, now + Duration::from_millis(240)),
            400.
        );
        assert_eq!(
            clip.height(Some(191.), false, now + Duration::from_millis(250)),
            400.
        );
        assert_eq!(
            clip.height(Some(191.), false, now + Duration::from_millis(500)),
            191.
        );
        assert_eq!(
            clip.height(Some(0.), true, now + Duration::from_millis(510)),
            0.
        );
        assert!(clip.row.started.is_none());
        tree.forget("a");
        assert_eq!(tree.visible_sessions("a", 12), 5);
    }
}
