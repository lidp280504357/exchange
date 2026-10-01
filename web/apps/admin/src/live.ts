import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

// What the console keeps up to date by itself: the counts waiting for the
// administrator (pushed on the event stream) and the console's settings.

export type Todo = AdminSchemas["Todo"];
export type ConsoleSettings = AdminSchemas["Settings"];

export const todoKey = ["admin", "todo"] as const;
export const settingsKey = ["admin", "settings"] as const;

const fetchTodo = async (): Promise<Todo> => adminData(await adminApi.GET("/admin/v1/todo"));

/**
 * useTodoStream opens the event stream (GET /admin/v1/events) once for the
 * console: its counts go to the query cache as they change; while it is
 * down they are polled every 15 seconds; signed_out ends the session in
 * the page.
 */
export function useTodoStream() {
  const qc = useQueryClient();
  const [live, setLive] = useState(false);
  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const es = new EventSource("/admin/v1/events");
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    es.addEventListener("todo", (e) => qc.setQueryData(todoKey, JSON.parse((e as MessageEvent<string>).data) as Todo));
    es.addEventListener("signed_out", () => {
      es.close();
      qc.setQueryData(["admin", "me"], null);
    });
    return () => es.close();
  }, [qc]);
  useQuery({ queryKey: todoKey, queryFn: fetchTodo, refetchInterval: live ? false : 15_000 });
}

/** useTodo reads the counts the stream keeps (fetched once when there are none yet). */
export function useTodo(): Todo | undefined {
  return useQuery({ queryKey: todoKey, queryFn: fetchTodo, staleTime: Infinity }).data;
}

/** useConsoleSettings reads the console's settings: two-person approval and the single-person limits. */
export function useConsoleSettings() {
  return useQuery({
    queryKey: settingsKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/settings")),
    staleTime: 30_000,
  });
}
