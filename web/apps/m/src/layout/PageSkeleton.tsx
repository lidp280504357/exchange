import { Skeleton, SkeletonLines } from "@exchange/ui";

/** PageSkeleton holds a page's place while its chunk loads. */
export function PageSkeleton() {
  return (
    <div className="flex flex-col gap-4 px-4 py-4">
      <Skeleton className="h-24 w-full rounded-3" />
      <SkeletonLines lines={6} />
    </div>
  );
}
