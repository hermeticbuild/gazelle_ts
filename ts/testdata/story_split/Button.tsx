import React from "react";

export interface ButtonProps {
  label: string;
}

export function Button({ label }: ButtonProps): React.ReactElement {
  return <button type="button">{label}</button>;
}
