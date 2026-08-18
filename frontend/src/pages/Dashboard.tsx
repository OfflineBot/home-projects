import Board from "../components/board/Board";

/**
 * The front page.
 *
 * It is a board like any other — the same tabs, the same cards, the same edit
 * switch — so it has no head of its own beyond the board's.
 */
export default function Dashboard() {
  return (
    <Board
      title="Dashboard"
      emptyNote="Nothing on your board yet — put the things you look at every day on it."
    />
  );
}
