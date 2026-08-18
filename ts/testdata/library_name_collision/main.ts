import { greet } from "./helper";

function main(argv: string[]) {
  console.log(greet(argv[0] ?? "world"));
}

main(process.argv.slice(2));
