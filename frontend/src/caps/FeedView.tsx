import { ErrorBox, Empty, Spinner, formatDate } from "../components/ui";
import { type Project } from "../lib/api";
import { useQuery } from "../lib/store";

/** Entries from a source, read out of feed.json. */
export default function FeedView({ project }: { project: Project; reload: () => void }) {
  const { data, error, loading, reload } = useQuery<{
    title: string;
    source: string;
    fetchedAt: string;
    entries: { title: string; url: string; published: string; summary?: string; file?: string }[];
  }>(`/api/projects/${project.id}/feed/entries`);

  return (
    <div>
      <ErrorBox error={error} onRetry={reload} />
      {loading && !data ? <Spinner /> : null}
      {data?.source ? (
        <p style={{ color: "var(--ctp-subtext0)", marginTop: 0 }}>
          {data.source} · fetched {formatDate(data.fetchedAt)}
        </p>
      ) : null}
      {data && data.entries.length === 0 ? (
        <Empty icon="rss">Nothing fetched yet. Add a feed scheduler for this project.</Empty>
      ) : null}
      <div className="list">
        {data?.entries.map((e) => (
          <a key={e.url + e.published} className="list-row" href={e.url} target="_blank" rel="noreferrer">
            <span className="meta" style={{ minWidth: 120 }}>{formatDate(e.published, false)}</span>
            <span className="grow">{e.title}</span>
            {e.file ? <span className="badge">saved</span> : null}
          </a>
        ))}
      </div>
    </div>
  );
}
