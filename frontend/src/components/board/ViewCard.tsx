import { Suspense } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../Icon";
import { Spinner } from "../ui";
import { capabilityViews } from "../../caps/index";
import FilesView from "../../caps/FilesView";
import { useQuery } from "../../lib/store";
import type { Project } from "../../lib/api";
import type { CardProps } from "./cards";

/**
 * A project's own view, on the board.
 *
 * This is what makes a board a home page rather than a list of numbers: the
 * mail is readable here, the calendar is usable here, the files are here. It
 * is the same component the project page draws — not a copy, not a summary —
 * so anything that works there works here.
 */
export default function ViewCard({ options, editing }: CardProps) {
  const id = String(options.projectId ?? "");
  const which = String(options.view ?? "files");
  const { data, loading } = useQuery<Project>(id ? `/api/projects/${id}` : null);

  if (!id) return <div className="meta">This card has no project yet.</div>;
  if (loading && !data) return <Spinner />;
  if (!data) return <div className="meta">That project is gone.</div>;

  const View = which === "files" ? FilesView : capabilityViews[which]?.component;
  const folder = String(options.folder ?? "");
  const address = `/groups/${data.groupSlug}/${data.slug}`;

  return (
    <div className="card-view">
      <div className="card-view-head">
        <Link to={address}>
          <Icon name={data.icon} size={14} /> {options.title || data.title}
        </Link>
        <span className="grow" />
        <Link className="meta" to={`${address}?tab=${which}${folder ? `&path=${encodeURIComponent(folder)}` : ""}`}>
          open it
        </Link>
      </div>
      <div className="card-view-body">
        {View ? (
          <Suspense fallback={<Spinner />}>
            <View project={data} reload={() => {}} start={folder || undefined} />
          </Suspense>
        ) : (
          <div className="meta">This project has no view called “{which}”.</div>
        )}
      </div>
      {editing ? <div className="card-view-shield" /> : null}
    </div>
  );
}
