import Board from "../components/board/Board";

/**
 * The front page.
 *
 * It is a board like any other — the same tabs, the same cards, the same edit
 * switch. What used to be here was a fixed list of tiles plus everything every
 * project reported, which is why it grew confusing: a page that shows
 * everything shows nothing in particular.
 */
export default function Dashboard() {
  return (
    <>
      <div className="page-head">
        <div>
          <h1>Dashboard</h1>
        </div>
      </div>
      <Board emptyNote="Nothing on your board yet — put the things you look at every day on it." />
    </>
  );
}
