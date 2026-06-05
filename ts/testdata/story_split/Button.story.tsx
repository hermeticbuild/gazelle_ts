import type { Meta, StoryObj } from "@storybook/react";
import React from "react";

import { Button } from "./Button";

const meta: Meta<typeof Button> = {
  component: Button,
  title: "Components/Button",
};

export default meta;

export const Primary: StoryObj<typeof Button> = {
  args: {
    label: "Primary",
  },
};
