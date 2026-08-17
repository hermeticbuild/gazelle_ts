import "node:process";

function main(args: string[]) {
  console.log(args);
}

main(process.argv.slice(2));
