// The frontend registry. One line per capability — the mirror of the one line
// in the backend's registry.
//
// Components are loaded lazily, so a capability nobody switched on costs
// nothing. Deleting a capability means deleting its file and its line here.

import { lazy, type ComponentType } from "react";
import type { Project } from "../lib/api";

export interface CapabilityViewProps {
  project: Project;
  reload: () => void;
}

export interface CapabilityView {
  tab: string;
  icon: string;
  component: ComponentType<CapabilityViewProps>;
}

export const capabilityViews: Record<string, CapabilityView> = {
  calendar: { tab: "Calendar", icon: "calendar", component: lazy(() => import("./CalendarView")) },
  markdown: { tab: "Notes", icon: "notebook", component: lazy(() => import("./MarkdownView")) },
  grades: { tab: "Grades", icon: "award", component: lazy(() => import("./GradesView")) },
  site: { tab: "Site", icon: "globe", component: lazy(() => import("./SiteView")) },
  mail: { tab: "Mail", icon: "mail", component: lazy(() => import("./MailView")) },
  feed: { tab: "Feed", icon: "rss", component: lazy(() => import("./FeedView")) },
  automation: { tab: "Rules", icon: "zap", component: lazy(() => import("./AutomationView")) },
  moodle: { tab: "Moodle", icon: "graduation", component: lazy(() => import("./MoodleView")) },
};
