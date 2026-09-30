import { Skeleton, SkeletonLines } from "@exchange/ui";

/** PageSkeleton holds a page's place while its chunk loads (within 200 ms of a click). */
export function PageSkeleton() {
  return (
    <div className="mx-auto flex max-w-[1440px] flex-col gap-6 px-6 py-8">
      <Skeleton className="h-8 w-64" />
      <div className="grid grid-cols-3 gap-4">
        {[0, 1, 2].map((i) => (
          <div key={i} className="rounded-3 bg-bg-1 p-4">
            <SkeletonLines lines={4} />
          </div>
        ))}
      </div>
      <SkeletonLines lines={6} />
    </div>
  );
}
