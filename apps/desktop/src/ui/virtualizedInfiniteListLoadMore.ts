export type LoadMoreDecision = Readonly<{ shouldLoad: boolean; lastLoadMoreKey: string }>;

// resolveLoadMore decides whether to request the next page. loadMoreKey only
// advances when a fetch succeeds, so a fetch that settles without advancing it
// (failed or canceled) releases the suppression — otherwise the same key would
// stay permanently blocked and scrolling could never retry the failed page.
export function resolveLoadMore({
  atBottom,
  hasNextPage,
  isFetchingNextPage,
  lastLoadMoreKey,
  loadMoreKey,
  wasFetchingNextPage,
}: Readonly<{
  atBottom: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  lastLoadMoreKey: string;
  loadMoreKey: string;
  wasFetchingNextPage: boolean;
}>): LoadMoreDecision {
  if (wasFetchingNextPage && !isFetchingNextPage && lastLoadMoreKey === loadMoreKey) {
    return { shouldLoad: false, lastLoadMoreKey: "" };
  }
  if (atBottom && hasNextPage && !isFetchingNextPage && lastLoadMoreKey !== loadMoreKey) {
    return { shouldLoad: true, lastLoadMoreKey: loadMoreKey };
  }
  return { shouldLoad: false, lastLoadMoreKey };
}
