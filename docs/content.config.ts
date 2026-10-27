import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { docsSchema } from "@astrojs/starlight/schema";
import { z } from "astro/zod";

const docs = defineCollection({
  loader: glob({
    base: "./docs",
    pattern: "**/*.md",
    generateId: ({ entry }) => entry === "README.md"
      ? "index"
      : entry.replace(/^content\/docs\//, "").replace(/\.md$/, ""),
  }),
  schema: docsSchema({
    extend: z.object({
      agent: z.object({
        type: z.string().min(1).optional(),
        prominent: z.boolean().optional(),
        dataUpdatedAt: z.string().datetime().optional(),
        binding: z.discriminatedUnion("kind", [
          z.object({ kind: z.literal("dashboard-catalog") }),
          z.object({ kind: z.literal("dashboard-page"), id: z.string().min(1) }),
          z.object({ kind: z.literal("dashboard-query"), id: z.string().min(1) }),
        ]).optional(),
        links: z.array(z.object({
          rel: z.string().min(1),
          href: z.string().min(1),
        })).optional(),
      }).optional(),
    }),
  }),
});

export const collections = { docs };